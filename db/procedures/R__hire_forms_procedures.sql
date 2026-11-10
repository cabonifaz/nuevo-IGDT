-- =====================================================================
-- Fichas de ingreso: borrador, corrección y revocación del ingreso confirmado, configuración, envíos y enlace con BDT.
-- =====================================================================
DELIMITER $$

-- Guarda el borrador de las fichas (la solicitud debe estar abierta). Si estaba pendiente, el gestor la toma.
-- p_pay_enc NULL conserva la remuneración del borrador anterior.
DROP PROCEDURE IF EXISTS sp_hire_draft_save $$
CREATE PROCEDURE sp_hire_draft_save(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_draft JSON, IN p_pay_enc VARBINARY(1024), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_status VARCHAR(12);
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  SET v_status = (SELECT status FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid);
  IF v_status IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status NOT IN ('PENDING', 'IN_PROGRESS') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE'; END IF;
  UPDATE hire_request SET draft = p_draft, draft_pay_enc = IFNULL(p_pay_enc, draft_pay_enc), draft_at = NOW(3), draft_by = p_actor_id,
         status = 'IN_PROGRESS', assigned_user_id = IFNULL(assigned_user_id, p_actor_id), taken_at = IFNULL(taken_at, NOW(3))
   WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'DRAFT', 'hire_request', p_request_uuid, NULL, p_ip);
END $$

-- Borrador de la solicitud (remuneración cifrada en base64; la API la descifra si el usuario puede verla).
DROP PROCEDURE IF EXISTS sp_hire_draft_get $$
CREATE PROCEDURE sp_hire_draft_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('forms', h.draft, 'payEnc', TO_BASE64(h.draft_pay_enc), 'at', h.draft_at,
           'byNameEnc', TO_BASE64(u.full_name_enc)) AS doc
    FROM hire_request h LEFT JOIN app_user u ON u.id = h.draft_by
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid AND h.draft IS NOT NULL;
END $$

-- Ventana de corrección del ingreso: activo y hasta la fecha de inicio más los días de gracia del tenant.
DROP PROCEDURE IF EXISTS sp_hire_assert_editable $$
CREATE PROCEDURE sp_hire_assert_editable(IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36), OUT p_hire_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_status VARCHAR(10);
  DECLARE v_start DATE;
  DECLARE v_grace INT;
  SET p_hire_id = NULL;
  SELECT x.id, x.status, x.start_date INTO p_hire_id, v_status, v_start
    FROM hire_request h JOIN hire x ON x.id = h.hire_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid FOR UPDATE;
  IF p_hire_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  SET v_grace = IFNULL((SELECT edit_grace_days FROM hire_form_setting WHERE tenant_id = p_tenant_id), 5);
  IF v_status <> 'ACTIVE' OR UTC_DATE() > DATE_ADD(v_start, INTERVAL v_grace DAY) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_LOCKED';
  END IF;
END $$

-- Corrige el ingreso confirmado. p_changes: diferencias calculadas por la API ([{"field","from","to"}]).
-- Si cambia la fecha de inicio, el motivo es obligatorio y BDT recibe START_CHANGED. p_pay_enc NULL = sin cambios.
DROP PROCEDURE IF EXISTS sp_hire_update $$
CREATE PROCEDURE sp_hire_update(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_position VARCHAR(150), IN p_contract_uuid CHAR(36), IN p_reason_uuid CHAR(36), IN p_area_uuid CHAR(36), IN p_site_uuid CHAR(36),
  IN p_start DATE, IN p_end DATE, IN p_schedule VARCHAR(200), IN p_object VARCHAR(1000), IN p_project VARCHAR(200),
  IN p_location VARCHAR(200), IN p_sunat TINYINT, IN p_has_equipment TINYINT, IN p_equipment JSON, IN p_equipment_request JSON,
  IN p_user_request JSON, IN p_currency_uuid CHAR(36), IN p_pay_enc VARBINARY(1024), IN p_notes VARCHAR(1000),
  IN p_change_reason VARCHAR(500), IN p_changes JSON, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_hire, v_contract, v_reason, v_area, v_site, v_currency BIGINT UNSIGNED;
  DECLARE v_old_start DATE;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  IF TRIM(IFNULL(p_position, '')) = '' OR TRIM(IFNULL(p_schedule, '')) = '' OR p_start IS NULL
     OR (p_end IS NOT NULL AND p_end < p_start) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  CALL sp_catalog_item_resolve(p_tenant_id, 'CONTRACT_MODALITY', p_contract_uuid, v_contract);
  CALL sp_catalog_item_resolve(p_tenant_id, 'HIRE_REASON', p_reason_uuid, v_reason);
  CALL sp_catalog_item_resolve(p_tenant_id, 'AREA', p_area_uuid, v_area);
  CALL sp_catalog_item_resolve(p_tenant_id, 'CURRENCY', p_currency_uuid, v_currency);
  IF p_site_uuid IS NOT NULL THEN CALL sp_catalog_item_resolve(p_tenant_id, 'WORK_SITE', p_site_uuid, v_site); END IF;
  START TRANSACTION;
  CALL sp_hire_assert_editable(p_tenant_id, p_request_uuid, v_hire);
  SET v_old_start = (SELECT start_date FROM hire WHERE id = v_hire);
  IF v_old_start <> p_start AND TRIM(IFNULL(p_change_reason, '')) = '' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:REASON_REQUIRED';
  END IF;
  UPDATE hire SET position = TRIM(p_position), contract_modality_item_id = v_contract, hire_reason_item_id = v_reason,
         area_item_id = v_area, work_site_item_id = v_site, start_date = p_start, end_date = p_end, schedule = TRIM(p_schedule),
         contract_object = NULLIF(TRIM(p_object), ''), project_service = NULLIF(TRIM(p_project), ''),
         location = NULLIF(TRIM(p_location), ''), sunat_declared = IFNULL(p_sunat, 0), has_equipment = IFNULL(p_has_equipment, 0),
         equipment = IF(p_has_equipment = 1, NULL, p_equipment), equipment_request = p_equipment_request, user_request = p_user_request,
         currency_item_id = v_currency, pay_enc = IFNULL(p_pay_enc, pay_enc), notes = NULLIF(TRIM(p_notes), ''),
         updated_at = NOW(3), updated_by = p_actor_id
   WHERE id = v_hire;
  INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
  VALUES (p_tenant_id, v_hire, IF(v_old_start <> p_start, 'START_CHANGED', 'UPDATED'), IFNULL(p_changes, JSON_ARRAY()),
          NULLIF(TRIM(p_change_reason), ''), p_actor_id);
  IF v_old_start <> p_start THEN
    INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
    SELECT p_tenant_id, 'START_CHANGED', h.candidate_uuid, h.requirement_uuid,
           JSON_OBJECT('hireId', x.uuid, 'startDate', p_start, 'previousStartDate', v_old_start, 'endDate', p_end,
                       'reason', TRIM(p_change_reason))
      FROM hire x JOIN hire_request h ON h.id = x.hire_request_id WHERE x.id = v_hire;
  END IF;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPDATE', 'hire_request', p_request_uuid,
                       JSON_OBJECT('startChanged', v_old_start <> p_start), p_ip);
END $$

-- Revoca el ingreso (el cliente lo anuló antes de empezar o en los días de gracia). BDT recibe REVOKED.
DROP PROCEDURE IF EXISTS sp_hire_revoke $$
CREATE PROCEDURE sp_hire_revoke(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_reason VARCHAR(500), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_hire, v_emp BIGINT UNSIGNED;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  IF TRIM(IFNULL(p_reason, '')) = '' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:REASON_REQUIRED'; END IF;
  START TRANSACTION;
  CALL sp_hire_assert_editable(p_tenant_id, p_request_uuid, v_hire);
  SET v_emp = (SELECT employee_id FROM hire WHERE id = v_hire);
  UPDATE hire SET status = 'REVOKED', revoked_at = NOW(3), revoked_by = p_actor_id, revoke_reason = TRIM(p_reason) WHERE id = v_hire;
  UPDATE hire_request SET status = 'REVOKED', revoked_at = NOW(3) WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid;
  -- el colaborador deja de estar activo si no tiene otro ingreso vigente
  UPDATE employee SET status = 'TERMINATED', updated_by = p_actor_id
   WHERE id = v_emp AND NOT EXISTS (SELECT 1 FROM hire WHERE employee_id = v_emp AND status = 'ACTIVE');
  INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
  VALUES (p_tenant_id, v_hire, 'REVOKED', JSON_ARRAY(), TRIM(p_reason), p_actor_id);
  INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
  SELECT p_tenant_id, 'REVOKED', h.candidate_uuid, h.requirement_uuid,
         JSON_OBJECT('hireId', x.uuid, 'reason', TRIM(p_reason), 'startDate', x.start_date)
    FROM hire x JOIN hire_request h ON h.id = x.hire_request_id WHERE x.id = v_hire;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'REVOKE', 'hire_request', p_request_uuid, NULL, p_ip);
END $$

-- Configuración de las fichas del tenant (con valores por defecto si aún no se guardó).
DROP PROCEDURE IF EXISTS sp_hire_form_setting_get $$
CREATE PROCEDURE sp_hire_form_setting_get(IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  SELECT JSON_OBJECT('recipients', IFNULL(s.recipients, JSON_OBJECT('FMI', JSON_ARRAY(), 'EQUIPMENT', JSON_ARRAY(), 'USERS', JSON_ARRAY())),
           'ccManager', IFNULL(s.cc_manager, 1) = 1, 'editGraceDays', IFNULL(s.edit_grace_days, 5),
           'corporateDomain', s.corporate_domain, 'updatedAt', s.updated_at,
           'company', IFNULL(t.legal_name, t.name), 'taxId', t.tax_id) AS doc
    FROM tenant t LEFT JOIN hire_form_setting s ON s.tenant_id = t.id
   WHERE t.id = p_tenant_id;
END $$

DROP PROCEDURE IF EXISTS sp_hire_form_setting_save $$
CREATE PROCEDURE sp_hire_form_setting_save(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_recipients JSON,
  IN p_cc_manager TINYINT, IN p_grace INT, IN p_domain VARCHAR(120), IN p_ip VARCHAR(45))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.settings');
  IF p_grace IS NULL OR p_grace < 0 OR p_grace > 60 OR JSON_TYPE(p_recipients) <> 'OBJECT' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  INSERT INTO hire_form_setting (tenant_id, recipients, cc_manager, edit_grace_days, corporate_domain, updated_by)
  VALUES (p_tenant_id, p_recipients, IFNULL(p_cc_manager, 1), p_grace, NULLIF(TRIM(p_domain), ''), p_actor_id)
  ON DUPLICATE KEY UPDATE recipients = p_recipients, cc_manager = IFNULL(p_cc_manager, 1), edit_grace_days = p_grace,
                          corporate_domain = NULLIF(TRIM(p_domain), ''), updated_by = p_actor_id;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPDATE', 'hire_form_setting', NULL, NULL, p_ip);
END $$

-- Registra el resultado del envío de una ficha.
DROP PROCEDURE IF EXISTS sp_hire_form_dispatch_add $$
CREATE PROCEDURE sp_hire_form_dispatch_add(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_form VARCHAR(10), IN p_reason VARCHAR(10), IN p_recipients JSON, IN p_status VARCHAR(14), IN p_error VARCHAR(500))
BEGIN
  INSERT INTO hire_form_dispatch (tenant_id, hire_id, form, reason, recipients, status, error, created_by)
  SELECT p_tenant_id, h.hire_id, p_form, p_reason, IFNULL(p_recipients, JSON_ARRAY()), p_status, LEFT(p_error, 500), NULLIF(p_actor_id, 0)
    FROM hire_request h WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid AND h.hire_id IS NOT NULL;
END $$

-- Enlace de lectura con BDT: BDT entrega (o retira) su llave con el token de integración del tenant.
DROP PROCEDURE IF EXISTS sp_bdt_link_set $$
CREATE PROCEDURE sp_bdt_link_set(IN p_tenant_id BIGINT UNSIGNED, IN p_api_key_id BIGINT UNSIGNED, IN p_key_enc VARBINARY(1024))
BEGIN
  INSERT INTO bdt_link (tenant_id, api_key_enc, api_key_id) VALUES (p_tenant_id, p_key_enc, p_api_key_id)
  ON DUPLICATE KEY UPDATE api_key_enc = p_key_enc, api_key_id = p_api_key_id, linked_at = NOW(3);
  CALL sp_audit_insert(p_tenant_id, NULL, p_api_key_id, 'LINK', 'bdt_link', NULL, NULL, NULL);
END $$

DROP PROCEDURE IF EXISTS sp_bdt_link_clear $$
CREATE PROCEDURE sp_bdt_link_clear(IN p_tenant_id BIGINT UNSIGNED, IN p_api_key_id BIGINT UNSIGNED)
BEGIN
  DELETE FROM bdt_link WHERE tenant_id = p_tenant_id;
  CALL sp_audit_insert(p_tenant_id, NULL, p_api_key_id, 'UNLINK', 'bdt_link', NULL, NULL, NULL);
END $$

DROP PROCEDURE IF EXISTS sp_bdt_link_get $$
CREATE PROCEDURE sp_bdt_link_get(IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  SELECT api_key_enc FROM bdt_link WHERE tenant_id = p_tenant_id;
END $$

-- Etiquetas (es) de ítems de catálogo por uuid: para imprimir las fichas y registrar los cambios con nombres legibles.
DROP PROCEDURE IF EXISTS sp_catalog_labels $$
CREATE PROCEDURE sp_catalog_labels(IN p_tenant_id BIGINT UNSIGNED, IN p_uuids JSON)
BEGIN
  SELECT i.uuid, i.code, IFNULL(JSON_UNQUOTE(JSON_EXTRACT(i.label_i18n, '$.es')), i.code) AS label, i.str1
    FROM JSON_TABLE(IFNULL(p_uuids, JSON_ARRAY()), '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
    JOIN master_item i ON i.uuid = j.u AND i.tenant_key IN (0, p_tenant_id);
END $$

DELIMITER ;
