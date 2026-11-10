-- =====================================================================
-- Registro del ingreso (desde la solicitud de ingreso), colaboradores y eventos para BDT.
-- =====================================================================
DELIMITER $$

-- Completa la solicitud: crea (o reactiva) el colaborador, registra el ingreso y deja el evento HIRED para BDT.
-- p_pay_enc: remuneración cifrada por la API. p_equipment: arreglo JSON de uuid de EQUIPMENT_TYPE (si no tiene equipo).
DROP PROCEDURE IF EXISTS sp_hire_complete $$
CREATE PROCEDURE sp_hire_complete(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_hire_uuid CHAR(36), IN p_employee_uuid CHAR(36), IN p_position VARCHAR(150), IN p_contract_uuid CHAR(36),
  IN p_reason_uuid CHAR(36), IN p_area_uuid CHAR(36), IN p_site_uuid CHAR(36), IN p_start DATE, IN p_end DATE,
  IN p_schedule VARCHAR(200), IN p_object VARCHAR(1000), IN p_project VARCHAR(200), IN p_location VARCHAR(200),
  IN p_sunat TINYINT, IN p_has_equipment TINYINT, IN p_equipment JSON, IN p_equipment_request JSON, IN p_user_request JSON,
  IN p_currency_uuid CHAR(36),
  IN p_pay_enc VARBINARY(1024), IN p_notes VARCHAR(1000), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_req, v_emp, v_hire, v_contract, v_reason, v_area, v_site, v_currency BIGINT UNSIGNED;
  DECLARE v_status VARCHAR(12);
  DECLARE v_n INT;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  IF TRIM(IFNULL(p_position, '')) = '' OR TRIM(IFNULL(p_schedule, '')) = '' OR p_start IS NULL
     OR (p_end IS NOT NULL AND p_end < p_start) OR p_pay_enc IS NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  CALL sp_catalog_item_resolve(p_tenant_id, 'CONTRACT_MODALITY', p_contract_uuid, v_contract);
  CALL sp_catalog_item_resolve(p_tenant_id, 'HIRE_REASON', p_reason_uuid, v_reason);
  CALL sp_catalog_item_resolve(p_tenant_id, 'AREA', p_area_uuid, v_area);
  CALL sp_catalog_item_resolve(p_tenant_id, 'CURRENCY', p_currency_uuid, v_currency);
  IF p_site_uuid IS NOT NULL THEN CALL sp_catalog_item_resolve(p_tenant_id, 'WORK_SITE', p_site_uuid, v_site); END IF;
  IF p_has_equipment = 0 AND p_equipment IS NOT NULL AND JSON_LENGTH(p_equipment) > 0 THEN
    SET v_n = (SELECT COUNT(DISTINCT i.id) FROM JSON_TABLE(p_equipment, '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
                 JOIN master_item i ON i.uuid = j.u AND i.tenant_key IN (0, p_tenant_id) AND i.deleted_at IS NULL AND i.is_active = 1
                 JOIN master_table t ON t.id = i.master_table_id AND t.code = 'EQUIPMENT_TYPE');
    IF v_n <> JSON_LENGTH(p_equipment) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  END IF;
  START TRANSACTION;
  SELECT id, status INTO v_req, v_status FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid FOR UPDATE;
  IF v_req IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status NOT IN ('PENDING', 'IN_PROGRESS') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE'; END IF;
  -- colaborador: uno por talento; si ya existía (reingreso) se actualizan sus datos y vuelve a estar activo
  SET v_emp = (SELECT e.id FROM employee e JOIN hire_request h ON h.talent_uuid = e.talent_uuid
                WHERE h.id = v_req AND e.tenant_id = p_tenant_id);
  IF v_emp IS NULL THEN
    INSERT INTO employee (uuid, tenant_id, talent_uuid, first_name, last_name, second_last_name, doc_type_code, doc_type_label,
                          doc_number_enc, doc_number_bidx, email_enc, phone_enc, created_by)
    SELECT p_employee_uuid, p_tenant_id, h.talent_uuid, h.first_name, h.last_name, h.second_last_name, h.doc_type_code, h.doc_type_label,
           h.doc_number_enc, h.doc_number_bidx, h.email_enc, h.phone_enc, p_actor_id
      FROM hire_request h WHERE h.id = v_req;
    SET v_emp = LAST_INSERT_ID();
  ELSE
    UPDATE employee e JOIN hire_request h ON h.id = v_req
       SET e.first_name = h.first_name, e.last_name = h.last_name, e.second_last_name = h.second_last_name,
           e.doc_type_code = h.doc_type_code, e.doc_type_label = h.doc_type_label, e.doc_number_enc = h.doc_number_enc,
           e.doc_number_bidx = h.doc_number_bidx, e.email_enc = h.email_enc, e.phone_enc = h.phone_enc,
           e.status = 'ACTIVE', e.updated_by = p_actor_id
     WHERE e.id = v_emp;
  END IF;
  INSERT INTO hire (uuid, tenant_id, hire_request_id, employee_id, client_uuid, client_label, position, contract_modality_item_id,
                    hire_reason_item_id, area_item_id, work_site_item_id, start_date, end_date, schedule, contract_object,
                    project_service, location, sunat_declared, has_equipment, equipment, equipment_request, user_request, currency_item_id, pay_enc,
                    notes, created_by)
  SELECT p_hire_uuid, p_tenant_id, v_req, v_emp, h.client_uuid, h.client_label, TRIM(p_position), v_contract, v_reason, v_area, v_site,
         p_start, p_end, TRIM(p_schedule), NULLIF(TRIM(p_object), ''), NULLIF(TRIM(p_project), ''), NULLIF(TRIM(p_location), ''),
         IFNULL(p_sunat, 0), IFNULL(p_has_equipment, 0), IF(p_has_equipment = 1, NULL, p_equipment), p_equipment_request, p_user_request,
         v_currency, p_pay_enc,
         NULLIF(TRIM(p_notes), ''), p_actor_id
    FROM hire_request h WHERE h.id = v_req;
  SET v_hire = LAST_INSERT_ID();
  UPDATE hire_request SET status = 'COMPLETED', completed_at = NOW(3), hire_id = v_hire, draft = NULL, draft_pay_enc = NULL,
         assigned_user_id = IFNULL(assigned_user_id, p_actor_id), taken_at = IFNULL(taken_at, NOW(3))
   WHERE id = v_req;
  INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
  SELECT p_tenant_id, 'HIRED', h.candidate_uuid, h.requirement_uuid,
         JSON_OBJECT('hireId', p_hire_uuid, 'employeeId', (SELECT uuid FROM employee WHERE id = v_emp), 'talentId', h.talent_uuid,
                     'position', TRIM(p_position), 'startDate', p_start, 'endDate', p_end,
                     'contract', (SELECT code FROM master_item WHERE id = v_contract))
    FROM hire_request h WHERE h.id = v_req;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'COMPLETE', 'hire_request', p_request_uuid, JSON_OBJECT('hire', p_hire_uuid), p_ip);
  SELECT p_hire_uuid AS uuid;
END $$

-- Ingreso registrado de una solicitud (doc JSON con etiquetas; payEnc en base64 para que la API lo descifre).
DROP PROCEDURE IF EXISTS sp_hire_of_request $$
CREATE PROCEDURE sp_hire_of_request(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('id', x.uuid, 'employeeId', e.uuid, 'position', x.position,
           'contract', JSON_OBJECT('id', c.uuid, 'code', c.code, 'label', JSON_UNQUOTE(JSON_EXTRACT(c.label_i18n, '$.es')), 'group', c.str1),
           'reason', JSON_OBJECT('id', r.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(r.label_i18n, '$.es'))),
           'area', JSON_OBJECT('id', a.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(a.label_i18n, '$.es'))),
           'site', IF(s.id IS NULL, NULL, JSON_OBJECT('id', s.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(s.label_i18n, '$.es')))),
           'startDate', x.start_date, 'endDate', x.end_date, 'schedule', x.schedule, 'contractObject', x.contract_object,
           'projectService', x.project_service, 'location', x.location, 'sunatDeclared', x.sunat_declared = 1,
           'hasEquipment', x.has_equipment = 1,
           'equipment', IFNULL((SELECT JSON_ARRAYAGG(JSON_UNQUOTE(JSON_EXTRACT(i.label_i18n, '$.es')))
                                  FROM JSON_TABLE(IFNULL(x.equipment, JSON_ARRAY()), '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
                                  JOIN master_item i ON i.uuid = j.u), JSON_ARRAY()),
           'equipmentIds', IFNULL(x.equipment, JSON_ARRAY()),
           'equipmentRequest', x.equipment_request, 'userRequest', x.user_request,
           'currency', JSON_OBJECT('id', cu.uuid, 'code', cu.code), 'payEnc', TO_BASE64(x.pay_enc), 'notes', x.notes,
           'createdAt', x.created_at, 'createdByNameEnc', TO_BASE64(u.full_name_enc),
           'updatedAt', x.updated_at, 'updatedByNameEnc', TO_BASE64(uu.full_name_enc),
           'signerId', IFNULL(uu.uuid, u.uuid), 'signerNameEnc', TO_BASE64(IFNULL(uu.full_name_enc, u.full_name_enc)),
           'status', x.status, 'revokedAt', x.revoked_at, 'revokeReason', x.revoke_reason, 'revokedByNameEnc', TO_BASE64(ur.full_name_enc),
           'editableUntil', DATE_ADD(x.start_date, INTERVAL IFNULL(st.edit_grace_days, 5) DAY),
           'editable', x.status = 'ACTIVE' AND UTC_DATE() <= DATE_ADD(x.start_date, INTERVAL IFNULL(st.edit_grace_days, 5) DAY),
           'changes', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('type', hc.type, 'changes', hc.changes, 'reason', hc.reason,
                                                               'at', hc.created_at, 'byNameEnc', TO_BASE64(cu2.full_name_enc)))
                                FROM (SELECT * FROM hire_change WHERE hire_id = x.id ORDER BY id DESC LIMIT 100) hc
                                LEFT JOIN app_user cu2 ON cu2.id = hc.created_by), JSON_ARRAY()),
           'dispatches', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('form', d.form, 'reason', d.reason, 'recipients', d.recipients,
                                                                  'status', d.status, 'error', d.error, 'at', d.created_at))
                                   FROM (SELECT * FROM hire_form_dispatch WHERE hire_id = x.id ORDER BY id DESC LIMIT 60) d), JSON_ARRAY())) AS doc
    FROM hire_request h
    JOIN hire x ON x.id = h.hire_id
    JOIN employee e ON e.id = x.employee_id
    JOIN master_item c ON c.id = x.contract_modality_item_id
    JOIN master_item r ON r.id = x.hire_reason_item_id
    JOIN master_item a ON a.id = x.area_item_id
    JOIN master_item cu ON cu.id = x.currency_item_id
    LEFT JOIN master_item s ON s.id = x.work_site_item_id
    LEFT JOIN app_user u ON u.id = x.created_by
    LEFT JOIN app_user uu ON uu.id = x.updated_by
    LEFT JOIN app_user ur ON ur.id = x.revoked_by
    LEFT JOIN hire_form_setting st ON st.tenant_id = h.tenant_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid;
END $$

-- Colaboradores con su último ingreso.
DROP PROCEDURE IF EXISTS sp_employee_list $$
CREATE PROCEDURE sp_employee_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'employee.read');
  SELECT JSON_OBJECT('id', e.uuid, 'talentId', e.talent_uuid, 'firstName', e.first_name, 'lastName', e.last_name,
           'secondLastName', e.second_last_name, 'docType', e.doc_type_label, 'status', e.status,
           'hire', JSON_OBJECT('id', x.uuid, 'requestId', h.uuid, 'client', x.client_label, 'position', x.position,
                               'contract', JSON_UNQUOTE(JSON_EXTRACT(c.label_i18n, '$.es')),
                               'area', JSON_UNQUOTE(JSON_EXTRACT(a.label_i18n, '$.es')),
                               'startDate', x.start_date, 'endDate', x.end_date)) AS doc
    FROM employee e
    JOIN hire x ON x.id = (SELECT MAX(x2.id) FROM hire x2 WHERE x2.employee_id = e.id)
    JOIN hire_request h ON h.id = x.hire_request_id
    JOIN master_item c ON c.id = x.contract_modality_item_id
    JOIN master_item a ON a.id = x.area_item_id
   WHERE e.tenant_id = p_tenant_id
   ORDER BY e.status = 'ACTIVE' DESC, x.start_date DESC
   LIMIT 2000;
END $$

-- Eventos para BDT desde el cursor (API de integración, hires.read).
DROP PROCEDURE IF EXISTS sp_hire_events $$
CREATE PROCEDURE sp_hire_events(IN p_tenant_id BIGINT UNSIGNED, IN p_since BIGINT UNSIGNED, IN p_limit INT)
BEGIN
  SELECT ev.id AS cursor_id,
         JSON_MERGE_PATCH(ev.data, JSON_OBJECT('type', ev.type, 'candidateId', ev.candidate_uuid, 'requirementId', ev.requirement_uuid,
                          'at', DATE_FORMAT(ev.created_at, '%Y-%m-%dT%H:%i:%s.%fZ'))) AS doc
    FROM hire_event ev
   WHERE ev.tenant_id = p_tenant_id AND ev.id > p_since
   ORDER BY ev.id
   LIMIT p_limit;
END $$

DELIMITER ;
