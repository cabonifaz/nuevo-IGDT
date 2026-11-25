-- =====================================================================
-- Aprobación de ingresos por ficha y por los gestores del cliente (V0019).
-- Pueden actuar sobre una solicitud: los gestores principal y secundario de su cliente, quien tiene hire.assign
-- (administrador) o, si el cliente no tiene gestores, cualquier gestor con hire.write.
-- =====================================================================
DELIMITER $$

DROP PROCEDURE IF EXISTS sp_hire_assert_can_act $$
CREATE PROCEDURE sp_hire_assert_can_act(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_client_uuid CHAR(36))
BEGIN
  IF NOT EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.assign')
     AND EXISTS (SELECT 1 FROM client_manager WHERE tenant_id = p_tenant_id AND client_uuid = p_client_uuid)
     AND NOT EXISTS (SELECT 1 FROM client_manager WHERE tenant_id = p_tenant_id AND client_uuid = p_client_uuid AND user_id = p_actor_id) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_NOT_MANAGER';
  END IF;
END $$

-- Recalcula el estado: COMPLETED cuando están aprobadas FMI, USERS y (si aplica) EQUIPMENT; si no, IN_PROGRESS en cuanto
-- hay un borrador o una aprobación. La solicitud de equipo aplica según equipment_required o, si aún no se decidió, según
-- el borrador (fichas con equipment) o la condición laptop de BDT.
DROP PROCEDURE IF EXISTS sp_hire_recompute $$
CREATE PROCEDURE sp_hire_recompute(IN p_request_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_fmi, v_users, v_equip, v_need TINYINT;
  SELECT EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = p_request_id AND form = 'FMI'),
         EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = p_request_id AND form = 'USERS'),
         EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = p_request_id AND form = 'EQUIPMENT')
    INTO v_fmi, v_users, v_equip;
  SELECT COALESCE(h.equipment_required,
                  IF(h.draft IS NOT NULL, IF(JSON_TYPE(JSON_EXTRACT(h.draft, '$.equipment')) IN ('OBJECT'), 1, 0), NULL),
                  IF(JSON_EXTRACT(h.doc, '$.conditions.laptop') = CAST('true' AS JSON), 1, 0))
    INTO v_need FROM hire_request h WHERE h.id = p_request_id;
  UPDATE hire_request
     SET status = CASE WHEN status IN ('CANCELLED', 'REVOKED') THEN status
                       WHEN v_fmi AND v_users AND (v_need = 0 OR v_equip) THEN 'COMPLETED'
                       WHEN status = 'COMPLETED' OR status = 'PENDING' THEN 'IN_PROGRESS'
                       ELSE status END,
         completed_at = IF(v_fmi AND v_users AND (v_need = 0 OR v_equip), IFNULL(completed_at, NOW(3)), NULL)
   WHERE id = p_request_id;
END $$

-- Aprueba la ficha de usuarios o la de equipo (la FMI se aprueba con sp_hire_complete / sp_hire_update). La de equipo
-- exige la de usuarios aprobada. p_data: las fichas aprobadas (sin la remuneración). Devuelve si la solicitud quedó
-- completa y si fue la primera aprobación de esa ficha.
DROP PROCEDURE IF EXISTS sp_hire_form_approve $$
CREATE PROCEDURE sp_hire_form_approve(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_form VARCHAR(10), IN p_data JSON, IN p_equipment_required TINYINT, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_req, v_hire BIGINT UNSIGNED;
  DECLARE v_first TINYINT DEFAULT 0;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  CALL sp_hire_assert_assignee(p_actor_id, p_tenant_id, p_request_uuid, 'IN_PROGRESS,COMPLETED');
  IF p_form NOT IN ('USERS', 'EQUIPMENT') OR p_data IS NULL OR JSON_TYPE(p_data) <> 'OBJECT' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  START TRANSACTION;
  SELECT id, hire_id INTO v_req, v_hire FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid FOR UPDATE;
  IF p_form = 'EQUIPMENT' AND NOT EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = v_req AND form = 'USERS') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_USERS_FIRST';
  END IF;
  SET v_first = NOT EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = v_req AND form = p_form);
  INSERT INTO hire_form_approval (tenant_id, hire_request_id, form, data, approved_by)
  VALUES (p_tenant_id, v_req, p_form, p_data, p_actor_id)
  ON DUPLICATE KEY UPDATE data = p_data, updated_at = NOW(3), updated_by = p_actor_id;
  IF v_hire IS NOT NULL THEN
    IF p_form = 'USERS' THEN UPDATE hire SET user_request = JSON_EXTRACT(p_data, '$.users') WHERE id = v_hire; END IF;
    IF p_form = 'EQUIPMENT' THEN UPDATE hire SET equipment_request = JSON_EXTRACT(p_data, '$.equipment') WHERE id = v_hire; END IF;
  END IF;
  UPDATE hire_request SET equipment_required = IFNULL(p_equipment_required, equipment_required),
         status = IF(status = 'PENDING', 'IN_PROGRESS', status),
         assigned_user_id = IFNULL(assigned_user_id, p_actor_id), taken_at = IFNULL(taken_at, NOW(3))
   WHERE id = v_req;
  CALL sp_hire_recompute(v_req);
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'APPROVE', 'hire_request', p_request_uuid,
                       JSON_OBJECT('form', p_form, 'first', v_first = 1), p_ip);
  SELECT status = 'COMPLETED' AS completed, v_first AS first FROM hire_request WHERE id = v_req;
END $$

-- La solicitud de equipo no aplica (o vuelve a aplicar): se decide en la solicitud y se recalcula el estado.
DROP PROCEDURE IF EXISTS sp_hire_equipment_required $$
CREATE PROCEDURE sp_hire_equipment_required(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_required TINYINT, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_req BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  CALL sp_hire_assert_assignee(p_actor_id, p_tenant_id, p_request_uuid, 'IN_PROGRESS,COMPLETED');
  SELECT id INTO v_req FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_request_uuid;
  IF p_required = 0 AND EXISTS (SELECT 1 FROM hire_form_approval WHERE hire_request_id = v_req AND form = 'EQUIPMENT') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE';
  END IF;
  UPDATE hire_request SET equipment_required = IF(p_required = 1, 1, 0), status = IF(status = 'PENDING', 'IN_PROGRESS', status)
   WHERE id = v_req;
  CALL sp_hire_recompute(v_req);
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPDATE', 'hire_request', p_request_uuid,
                       JSON_OBJECT('equipmentRequired', p_required = 1), p_ip);
END $$

-- Aprobaciones de la solicitud: ficha, lo aprobado y quién (y cuándo) la aprobó o la corrigió por última vez.
DROP PROCEDURE IF EXISTS sp_hire_approvals $$
CREATE PROCEDURE sp_hire_approvals(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('form', a.form, 'data', a.data, 'approvedAt', a.approved_at, 'updatedAt', a.updated_at,
           'approvedBy', JSON_OBJECT('id', ab.uuid, 'nameEnc', TO_BASE64(ab.full_name_enc), 'emailEnc', TO_BASE64(ab.email_enc)),
           'updatedBy', IF(ub.id IS NULL, NULL, JSON_OBJECT('id', ub.uuid, 'nameEnc', TO_BASE64(ub.full_name_enc), 'emailEnc', TO_BASE64(ub.email_enc)))) AS doc
    FROM hire_form_approval a
    JOIN hire_request h ON h.id = a.hire_request_id AND h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid
    LEFT JOIN app_user ab ON ab.id = a.approved_by
    LEFT JOIN app_user ub ON ub.id = a.updated_by
   ORDER BY FIELD(a.form, 'FMI', 'USERS', 'EQUIPMENT');
END $$

-- ---------------------------------------------------------------------
-- Gestores por cliente
-- ---------------------------------------------------------------------

-- Gestores de todos los clientes del tenant (para la lista de clientes y la bandeja).
DROP PROCEDURE IF EXISTS sp_client_managers_list $$
CREATE PROCEDURE sp_client_managers_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  CALL sp_assert_member(p_actor_id, p_tenant_id);
  SELECT JSON_OBJECT('clientId', m.client_uuid, 'role', m.role, 'user', JSON_OBJECT('id', u.uuid, 'nameEnc', TO_BASE64(u.full_name_enc))) AS doc
    FROM client_manager m JOIN app_user u ON u.id = m.user_id
   WHERE m.tenant_id = p_tenant_id
   ORDER BY m.client_uuid, FIELD(m.role, 'PRIMARY', 'SECONDARY');
END $$

-- Asigna los gestores principal y secundario de un cliente (NULL = sin gestor en ese rol). Deben ser usuarios activos del
-- tenant con hire.write y distintos entre sí.
DROP PROCEDURE IF EXISTS sp_client_managers_set $$
CREATE PROCEDURE sp_client_managers_set(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_client_uuid CHAR(36),
  IN p_primary_uuid CHAR(36), IN p_secondary_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_client, v_primary, v_secondary BIGINT UNSIGNED;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'client.managers.manage');
  CALL sp_catalog_item_resolve(p_tenant_id, 'CLIENT', p_client_uuid, v_client);
  IF p_primary_uuid IS NOT NULL THEN
    SELECT u.id INTO v_primary FROM app_user u
     WHERE u.uuid = p_primary_uuid AND u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
       AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
    IF v_primary IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  END IF;
  IF p_secondary_uuid IS NOT NULL THEN
    SELECT u.id INTO v_secondary FROM app_user u
     WHERE u.uuid = p_secondary_uuid AND u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
       AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
    IF v_secondary IS NULL OR v_secondary <=> v_primary THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  END IF;
  START TRANSACTION;
  DELETE FROM client_manager WHERE tenant_id = p_tenant_id AND client_uuid = p_client_uuid;
  IF v_primary IS NOT NULL THEN
    INSERT INTO client_manager (tenant_id, client_uuid, role, user_id, updated_by) VALUES (p_tenant_id, p_client_uuid, 'PRIMARY', v_primary, p_actor_id);
  END IF;
  IF v_secondary IS NOT NULL THEN
    INSERT INTO client_manager (tenant_id, client_uuid, role, user_id, updated_by) VALUES (p_tenant_id, p_client_uuid, 'SECONDARY', v_secondary, p_actor_id);
  END IF;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPDATE', 'client_manager', p_client_uuid,
                       JSON_OBJECT('primary', p_primary_uuid, 'secondary', p_secondary_uuid), p_ip);
END $$

-- Usuarios que pueden ser gestores de un cliente (activos con hire.write).
DROP PROCEDURE IF EXISTS sp_client_manager_candidates $$
CREATE PROCEDURE sp_client_manager_candidates(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'client.managers.manage');
  SELECT JSON_OBJECT('id', u.uuid, 'nameEnc', TO_BASE64(u.full_name_enc)) AS doc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
END $$

-- ---------------------------------------------------------------------
-- Recordatorios de solicitudes pendientes
-- ---------------------------------------------------------------------

-- Tenants activos con la bandeja de selección habilitada, con su configuración de recordatorio.
DROP PROCEDURE IF EXISTS sp_hire_reminder_tenants $$
CREATE PROCEDURE sp_hire_reminder_tenants()
BEGIN
  SELECT t.id, t.slug, IFNULL(t.legal_name, t.name) AS company, IFNULL(s.reminder_days, 2) AS reminder_days
    FROM tenant t
    JOIN feature f ON f.code = 'selection_inbox'
    LEFT JOIN tenant_feature tf ON tf.tenant_id = t.id AND tf.feature_id = f.id
    LEFT JOIN hire_form_setting s ON s.tenant_id = t.id
   WHERE t.status = 'ACTIVE' AND t.deleted_at IS NULL AND IFNULL(tf.enabled, f.default_enabled) = 1;
END $$

-- Reserva la corrida del día (DAILY: una vez por día; EMAIL: cada p_every días). Devuelve claimed = 1 si le toca a esta
-- instancia (varias instancias de la API no duplican los avisos).
DROP PROCEDURE IF EXISTS sp_hire_reminder_claim $$
CREATE PROCEDURE sp_hire_reminder_claim(IN p_tenant_id BIGINT UNSIGNED, IN p_kind VARCHAR(10), IN p_today DATE, IN p_every INT)
BEGIN
  DECLARE v_claimed INT DEFAULT 0;
  INSERT IGNORE INTO hire_reminder_run (tenant_id, kind, last_run) VALUES (p_tenant_id, p_kind, p_today);
  IF ROW_COUNT() = 1 THEN
    SET v_claimed = 1;
  ELSE
    UPDATE hire_reminder_run SET last_run = p_today, ran_at = NOW(3)
     WHERE tenant_id = p_tenant_id AND kind = p_kind AND DATEDIFF(p_today, last_run) >= GREATEST(IFNULL(p_every, 1), 1);
    SET v_claimed = ROW_COUNT();
  END IF;
  SELECT v_claimed AS claimed;
END $$

-- Solicitudes pendientes (recibidas o en proceso) con su fecha de inicio, fichas aprobadas y gestores del cliente; y los
-- administradores (hire.assign), que reciben las de clientes sin gestores.
DROP PROCEDURE IF EXISTS sp_hire_pending $$
CREATE PROCEDURE sp_hire_pending(IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  SELECT JSON_OBJECT('id', h.uuid, 'status', h.status, 'receivedAt', h.received_at,
           'requirement', JSON_EXTRACT(h.doc, '$.requirement.code'), 'client', JSON_OBJECT('id', h.client_uuid, 'label', h.client_label),
           'talent', JSON_OBJECT('firstName', h.first_name, 'lastName', h.last_name, 'secondLastName', h.second_last_name),
           'startDate', COALESCE(x.start_date, STR_TO_DATE(JSON_UNQUOTE(JSON_EXTRACT(h.draft, '$.hire.startDate')), '%Y-%m-%d'),
                                 STR_TO_DATE(JSON_UNQUOTE(JSON_EXTRACT(h.doc, '$.conditions.startDate')), '%Y-%m-%d')),
           'equipmentRequired', COALESCE(h.equipment_required,
                                IF(h.draft IS NOT NULL, IF(JSON_TYPE(JSON_EXTRACT(h.draft, '$.equipment')) = 'OBJECT', 1, 0), NULL),
                                IF(JSON_EXTRACT(h.doc, '$.conditions.laptop') = CAST('true' AS JSON), 1, 0)) = 1,
           'approved', IFNULL((SELECT JSON_ARRAYAGG(a.form) FROM hire_form_approval a WHERE a.hire_request_id = h.id), JSON_ARRAY()),
           'managers', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', u.uuid, 'userId', u.id,
                                    'role', IFNULL((SELECT m.role FROM client_manager m WHERE m.tenant_id = h.tenant_id AND m.client_uuid = h.client_uuid AND m.user_id = u.id LIMIT 1), 'DIRECTED'),
                                    'nameEnc', TO_BASE64(u.full_name_enc), 'emailEnc', TO_BASE64(u.email_enc), 'locale', u.locale))
                                 FROM app_user u
                                WHERE u.status = 'ACTIVE' AND u.deleted_at IS NULL
                                  AND (u.id = h.directed_user_id
                                       OR u.id IN (SELECT m.user_id FROM client_manager m WHERE m.tenant_id = h.tenant_id AND m.client_uuid = h.client_uuid))), JSON_ARRAY())) AS doc
    FROM hire_request h
    LEFT JOIN hire x ON x.id = h.hire_id
   WHERE h.tenant_id = p_tenant_id AND h.status IN ('PENDING', 'IN_PROGRESS')
   ORDER BY h.received_at;
  SELECT JSON_OBJECT('id', u.uuid, 'userId', u.id, 'nameEnc', TO_BASE64(u.full_name_enc), 'emailEnc', TO_BASE64(u.email_enc), 'locale', u.locale) AS doc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.status = 'ACTIVE' AND u.deleted_at IS NULL
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.assign');
END $$

DELIMITER ;

DELIMITER $$
-- Para BDT (API de integración): gestores de IGDT a los que se puede dirigir una solicitud de ingreso y los gestores de
-- cada cliente.
DROP PROCEDURE IF EXISTS sp_integration_managers $$
CREATE PROCEDURE sp_integration_managers(IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  SELECT u.uuid, u.full_name_enc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
  SELECT m.client_uuid, m.role, u.uuid
    FROM client_manager m JOIN app_user u ON u.id = m.user_id
   WHERE m.tenant_id = p_tenant_id ORDER BY m.client_uuid, FIELD(m.role, 'PRIMARY', 'SECONDARY');
END $$
DELIMITER ;
