-- =====================================================================
-- Fin de contrato (V0021): alertas de contratos por terminar y operaciones del asistente (renovar, baja, traspaso y
-- cambio a otro RQ), equipo del colaborador y eventos para BDT.
-- Gestionan el fin de contrato los mismos que aprueban ingresos: los gestores del cliente ACTUAL del contrato (cambia con
-- un traspaso), el gestor dirigido de la solicitud o un administrador (hire.assign); si el cliente no tiene gestores,
-- cualquier gestor con hire.write.
-- =====================================================================
DELIMITER $$

-- El contrato de la solicitud debe estar vigente (ACTIVE) y el actor poder gestionarlo. Bloquea la fila del ingreso.
DROP PROCEDURE IF EXISTS sp_hire_assert_contract $$
CREATE PROCEDURE sp_hire_assert_contract(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  OUT p_hire_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_status VARCHAR(12);
  DECLARE v_client CHAR(36);
  DECLARE v_directed BIGINT UNSIGNED;
  SET p_hire_id = NULL;
  SELECT x.id, x.status, x.client_uuid, h.directed_user_id INTO p_hire_id, v_status, v_client, v_directed
    FROM hire_request h JOIN hire x ON x.id = h.hire_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid FOR UPDATE;
  IF p_hire_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status <> 'ACTIVE' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_CLOSED'; END IF;
  IF NOT (v_directed <=> p_actor_id) THEN
    CALL sp_hire_assert_can_act(p_actor_id, p_tenant_id, v_client);
  END IF;
END $$

-- Equipo entregado según la ficha de equipo aprobada del ingreso (hire.equipment_request): una fila por tipo de equipo y
-- el celular si se pidió. Se rehace mientras todo siga «entregado» (si ya hay algo por recuperar o recuperado, no se toca).
DROP PROCEDURE IF EXISTS sp_employee_equipment_sync $$
CREATE PROCEDURE sp_employee_equipment_sync(IN p_hire_id BIGINT UNSIGNED, IN p_actor_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_tenant, v_emp BIGINT UNSIGNED;
  DECLARE v_eq JSON;
  DECLARE v_delivered DATE;
  SELECT tenant_id, employee_id, equipment_request INTO v_tenant, v_emp, v_eq FROM hire WHERE id = p_hire_id;
  IF v_emp IS NOT NULL AND NOT EXISTS (SELECT 1 FROM employee_equipment WHERE hire_id = p_hire_id AND status <> 'DELIVERED') THEN
    DELETE FROM employee_equipment WHERE hire_id = p_hire_id;
    IF v_eq IS NOT NULL AND JSON_TYPE(v_eq) = 'OBJECT' THEN
      SET v_delivered = IF(JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.deliveryDate')) REGEXP '^[0-9]{4}-[0-9]{2}-[0-9]{2}$',
                           CAST(JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.deliveryDate')) AS DATE), NULL);
      INSERT INTO employee_equipment (uuid, tenant_id, employee_id, hire_id, type_item_id, label, details, status, delivered_at, created_by)
      SELECT UUID(), v_tenant, v_emp, p_hire_id, i.id, IFNULL(JSON_UNQUOTE(JSON_EXTRACT(i.label_i18n, '$.es')), i.code),
             JSON_OBJECT('processor', JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.processor')), 'ram', JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.ram')),
                         'disk', JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.disk')), 'brand', JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.brand')),
                         'accessories', JSON_UNQUOTE(JSON_EXTRACT(v_eq, '$.accessories'))),
             'DELIVERED', v_delivered, NULLIF(p_actor_id, 0)
        FROM JSON_TABLE(IFNULL(JSON_EXTRACT(v_eq, '$.types'), JSON_ARRAY()), '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
        JOIN master_item i ON i.uuid = j.u AND i.tenant_key IN (0, v_tenant);
      IF JSON_EXTRACT(v_eq, '$.mobile') = CAST('true' AS JSON) THEN
        INSERT INTO employee_equipment (uuid, tenant_id, employee_id, hire_id, type_item_id, label, status, delivered_at, created_by)
        VALUES (UUID(), v_tenant, v_emp, p_hire_id, NULL, 'Celular', 'DELIVERED', v_delivered, NULLIF(p_actor_id, 0));
      END IF;
    END IF;
  END IF;
END $$

-- Ejecuta una operación de fin de contrato sobre el ingreso de la solicitud (validada también en la API):
--   RENEW      p_end (> fin actual) y p_reason: extiende el contrato.                       Evento RENEWED.
--   TERMINATE  p_date (cese), p_term_reason_uuid (TERMINATION_REASON), p_recover y p_return_date: baja; el colaborador
--              queda TERMINATED si no tiene otro ingreso vigente y su equipo entregado pasa a «por recuperar».  TERMINATED.
--   TRANSFER   p_client_uuid (CLIENT), p_area_uuid (AREA), p_position, p_schedule, p_date (movimiento) y p_end (NULL =
--              indeterminado): el contrato pasa al otro cliente (sus gestores lo ven desde ahora).          TRANSFERRED.
--   RQ_CHANGE  p_rq_uuid (RQ abierto de BDT) y p_date (salida): cierra el contrato (RQ_CHANGED) a la espera del nuevo
--              ingreso que Selección gestione en BDT.                                                 RQ_CHANGE_REQUESTED.
-- p_data: entradas, etiquetas y la foto del contrato antes del cambio (para reimprimir). p_changes: [{"field","from","to"}].
DROP PROCEDURE IF EXISTS sp_hire_contract_op $$
CREATE PROCEDURE sp_hire_contract_op(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_op_uuid CHAR(36), IN p_type VARCHAR(10), IN p_date DATE, IN p_end DATE, IN p_term_reason_uuid CHAR(36), IN p_recover TINYINT,
  IN p_return_date DATE, IN p_client_uuid CHAR(36), IN p_area_uuid CHAR(36), IN p_position VARCHAR(150), IN p_schedule VARCHAR(200),
  IN p_rq_uuid CHAR(36), IN p_reason VARCHAR(500), IN p_data JSON, IN p_changes JSON, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_hire, v_emp, v_req, v_term, v_client_item, v_area BIGINT UNSIGNED;
  DECLARE v_start, v_end DATE;
  DECLARE v_old_client CHAR(36);
  DECLARE v_old_label, v_client_label VARCHAR(200);
  DECLARE v_change, v_event VARCHAR(20);
  DECLARE v_status VARCHAR(10) DEFAULT 'EXECUTED';
  DECLARE v_event_rq CHAR(36);
  DECLARE v_event_data JSON;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  IF p_type NOT IN ('RENEW', 'TERMINATE', 'TRANSFER', 'RQ_CHANGE') OR p_data IS NULL OR JSON_TYPE(p_data) <> 'OBJECT' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  IF p_type = 'TERMINATE' THEN CALL sp_catalog_item_resolve(p_tenant_id, 'TERMINATION_REASON', p_term_reason_uuid, v_term); END IF;
  IF p_type = 'TRANSFER' THEN
    CALL sp_catalog_item_resolve(p_tenant_id, 'CLIENT', p_client_uuid, v_client_item);
    CALL sp_catalog_item_resolve(p_tenant_id, 'AREA', p_area_uuid, v_area);
    SET v_client_label = (SELECT IFNULL(JSON_UNQUOTE(JSON_EXTRACT(label_i18n, '$.es')), code) FROM master_item WHERE id = v_client_item);
  END IF;
  START TRANSACTION;
  CALL sp_hire_assert_contract(p_actor_id, p_tenant_id, p_request_uuid, v_hire);
  SELECT start_date, end_date, employee_id, hire_request_id, client_uuid, client_label
    INTO v_start, v_end, v_emp, v_req, v_old_client, v_old_label FROM hire WHERE id = v_hire;
  IF p_type <> 'RENEW' AND (p_date IS NULL OR p_date < v_start) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_DATE_INVALID';
  END IF;
  IF p_type = 'RENEW' THEN
    IF p_end IS NULL OR p_end < v_start OR (v_end IS NOT NULL AND p_end <= v_end) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_DATE_INVALID';
    END IF;
    IF TRIM(IFNULL(p_reason, '')) = '' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:REASON_REQUIRED'; END IF;
    UPDATE hire SET end_date = p_end, updated_at = NOW(3), updated_by = p_actor_id WHERE id = v_hire;
    SET v_change = 'RENEWED', v_event = 'RENEWED',
        v_event_data = JSON_OBJECT('previousEndDate', v_end, 'endDate', p_end, 'reason', TRIM(p_reason));
  ELSEIF p_type = 'TERMINATE' THEN
    IF p_recover = 1 AND p_return_date IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
    UPDATE hire SET status = 'TERMINATED', termination_date = p_date, termination_reason_item_id = v_term,
           equipment_return_date = IF(p_recover = 1, p_return_date, NULL), closed_at = NOW(3), closed_by = p_actor_id,
           updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    UPDATE employee SET status = 'TERMINATED', updated_by = p_actor_id
     WHERE id = v_emp AND NOT EXISTS (SELECT 1 FROM hire WHERE employee_id = v_emp AND status = 'ACTIVE');
    IF p_recover = 1 THEN
      IF NOT EXISTS (SELECT 1 FROM employee_equipment WHERE hire_id = v_hire) THEN CALL sp_employee_equipment_sync(v_hire, p_actor_id); END IF;
      UPDATE employee_equipment SET status = 'PENDING_RETURN', return_due_date = p_return_date, updated_by = p_actor_id
       WHERE employee_id = v_emp AND status = 'DELIVERED';
    END IF;
    SET v_change = 'TERMINATED', v_event = 'TERMINATED',
        v_event_data = JSON_OBJECT('terminationDate', p_date,
                                   'reason', (SELECT JSON_OBJECT('id', uuid, 'code', code, 'label', JSON_UNQUOTE(JSON_EXTRACT(label_i18n, '$.es'))) FROM master_item WHERE id = v_term),
                                   'recoverEquipment', p_recover = 1, 'equipmentReturnDate', IF(p_recover = 1, p_return_date, NULL));
  ELSEIF p_type = 'TRANSFER' THEN
    IF TRIM(IFNULL(p_position, '')) = '' OR TRIM(IFNULL(p_schedule, '')) = '' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
    IF p_end IS NOT NULL AND p_end <= p_date THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_DATE_INVALID'; END IF;
    UPDATE hire SET client_uuid = p_client_uuid, client_label = v_client_label, area_item_id = v_area, position = TRIM(p_position),
           schedule = TRIM(p_schedule), end_date = p_end, updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    SET v_change = 'TRANSFERRED', v_event = 'TRANSFERRED',
        v_event_data = JSON_OBJECT('date', p_date, 'client', JSON_OBJECT('id', p_client_uuid, 'label', v_client_label),
                                   'previousClient', JSON_OBJECT('id', v_old_client, 'label', v_old_label),
                                   'position', TRIM(p_position), 'endDate', p_end, 'previousEndDate', v_end);
  ELSE
    IF NOT EXISTS (SELECT 1 FROM bdt_requirement WHERE tenant_id = p_tenant_id AND uuid = p_rq_uuid AND deleted_at IS NULL AND status_group = 'OPEN') THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:RQ_NOT_OPEN';
    END IF;
    UPDATE hire SET status = 'RQ_CHANGED', termination_date = p_date, closed_at = NOW(3), closed_by = p_actor_id,
           updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    SET v_change = 'RQ_CHANGED', v_event = 'RQ_CHANGE_REQUESTED', v_status = 'REQUESTED', v_event_rq = p_rq_uuid,
        v_event_data = (SELECT JSON_OBJECT('requirementCode', r.code, 'requirementTitle', r.title,
                                           'client', JSON_OBJECT('id', r.client_uuid, 'label', r.client_label), 'date', p_date,
                                           'fromRequirementId', h.requirement_uuid)
                          FROM bdt_requirement r JOIN hire_request h ON h.id = v_req
                         WHERE r.tenant_id = p_tenant_id AND r.uuid = p_rq_uuid);
  END IF;
  INSERT INTO hire_contract_op (uuid, tenant_id, hire_id, type, status, effective_date, data, reason, created_by)
  VALUES (p_op_uuid, p_tenant_id, v_hire, p_type, v_status, IFNULL(IF(p_type = 'RENEW', p_end, p_date), CURRENT_DATE), p_data,
          NULLIF(TRIM(p_reason), ''), p_actor_id);
  INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
  VALUES (p_tenant_id, v_hire, v_change, IFNULL(p_changes, JSON_ARRAY()), NULLIF(TRIM(p_reason), ''), p_actor_id);
  INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
  SELECT p_tenant_id, v_event, h.candidate_uuid, IFNULL(v_event_rq, h.requirement_uuid),
         JSON_MERGE_PATCH(JSON_OBJECT('hireId', x.uuid, 'employeeId', e.uuid, 'talentId', h.talent_uuid, 'operationId', p_op_uuid), v_event_data)
    FROM hire x JOIN hire_request h ON h.id = x.hire_request_id JOIN employee e ON e.id = x.employee_id WHERE x.id = v_hire;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, p_type, 'hire_request', p_request_uuid, JSON_OBJECT('operation', p_op_uuid), p_ip);
  SELECT p_op_uuid AS uuid;
END $$

-- Contexto del fin de contrato de una solicitud: contrato vigente, si el usuario puede gestionarlo, operaciones (con sus
-- envíos) y el equipo del colaborador.
DROP PROCEDURE IF EXISTS sp_hire_contract_get $$
CREATE PROCEDURE sp_hire_contract_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  DECLARE v_admin TINYINT;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SET v_admin = EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.assign');
  SELECT JSON_OBJECT('hireId', x.uuid, 'requestId', h.uuid, 'employeeId', e.uuid, 'status', x.status,
           'talent', JSON_OBJECT('firstName', e.first_name, 'lastName', e.last_name, 'secondLastName', e.second_last_name),
           'client', JSON_OBJECT('id', x.client_uuid, 'label', x.client_label), 'requirement', JSON_EXTRACT(h.doc, '$.requirement.code'),
           'requirementId', h.requirement_uuid,
           'position', x.position, 'area', JSON_OBJECT('id', a.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(a.label_i18n, '$.es'))),
           'schedule', x.schedule, 'startDate', x.start_date, 'endDate', x.end_date,
           'terminationDate', x.termination_date, 'equipmentReturnDate', x.equipment_return_date,
           'terminationReason', IF(tr.id IS NULL, NULL, JSON_OBJECT('id', tr.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(tr.label_i18n, '$.es')))),
           'canManage', x.status = 'ACTIVE' AND EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.write')
                        AND (v_admin = 1 OR h.directed_user_id <=> p_actor_id
                             OR NOT EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid)
                             OR EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid AND m.user_id = p_actor_id)),
           'users', x.user_request, 'equipmentRequest', x.equipment_request,
           'operations', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', o.uuid, 'type', o.type, 'status', o.status, 'effectiveDate', o.effective_date,
                                         'data', o.data, 'reason', o.reason, 'at', o.created_at, 'byNameEnc', TO_BASE64(ou.full_name_enc),
                                         'dispatches', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('form', d.form, 'reason', d.reason, 'recipients', d.recipients,
                                                                     'status', d.status, 'error', d.error, 'at', d.created_at))
                                                                 FROM hire_form_dispatch d WHERE d.contract_op_id = o.id), JSON_ARRAY())))
                                  FROM (SELECT * FROM hire_contract_op WHERE hire_id = x.id ORDER BY id DESC LIMIT 50) o
                                  LEFT JOIN app_user ou ON ou.id = o.created_by), JSON_ARRAY()),
           'equipment', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', q.uuid, 'label', q.label, 'details', q.details, 'status', q.status,
                                        'deliveredAt', q.delivered_at, 'returnDueDate', q.return_due_date, 'returnedAt', q.returned_at))
                                 FROM employee_equipment q WHERE q.employee_id = e.id AND q.status <> 'RETURNED'), JSON_ARRAY())) AS doc
    FROM hire_request h
    JOIN hire x ON x.id = h.hire_id
    JOIN employee e ON e.id = x.employee_id
    JOIN master_item a ON a.id = x.area_item_id
    LEFT JOIN master_item tr ON tr.id = x.termination_reason_item_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid;
END $$

-- Operación registrada (para reenviar sus documentos).
DROP PROCEDURE IF EXISTS sp_hire_contract_op_get $$
CREATE PROCEDURE sp_hire_contract_op_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36), IN p_op_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('id', o.uuid, 'type', o.type, 'status', o.status, 'effectiveDate', o.effective_date, 'data', o.data, 'reason', o.reason,
           'at', o.created_at) AS doc
    FROM hire_contract_op o JOIN hire x ON x.id = o.hire_id JOIN hire_request h ON h.id = x.hire_request_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid AND o.uuid = p_op_uuid;
END $$

-- Para reenviar: el actor debe poder gestionar el contrato (vigente o ya cerrado por la operación).
DROP PROCEDURE IF EXISTS sp_hire_contract_assert_manager $$
CREATE PROCEDURE sp_hire_contract_assert_manager(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  DECLARE v_client CHAR(36);
  DECLARE v_directed BIGINT UNSIGNED;
  DECLARE v_found TINYINT DEFAULT 0;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  SELECT 1, x.client_uuid, h.directed_user_id INTO v_found, v_client, v_directed
    FROM hire_request h JOIN hire x ON x.id = h.hire_id WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid;
  IF v_found = 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF NOT (v_directed <=> p_actor_id) THEN CALL sp_hire_assert_can_act(p_actor_id, p_tenant_id, v_client); END IF;
END $$

-- Contratos vigentes que terminan hasta p_today + p_days (o ya vencidos) que el usuario puede gestionar.
DROP PROCEDURE IF EXISTS sp_contract_alerts $$
CREATE PROCEDURE sp_contract_alerts(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_today DATE, IN p_days INT)
BEGIN
  DECLARE v_admin TINYINT;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SET v_admin = EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.assign');
  SELECT JSON_OBJECT('requestId', h.uuid, 'hireId', x.uuid, 'employeeId', e.uuid,
           'talent', JSON_OBJECT('firstName', e.first_name, 'lastName', e.last_name, 'secondLastName', e.second_last_name),
           'client', x.client_label, 'rq', JSON_UNQUOTE(JSON_EXTRACT(h.doc, '$.requirement.code')), 'position', x.position,
           'startDate', x.start_date, 'endDate', x.end_date) AS doc
    FROM hire x
    JOIN hire_request h ON h.id = x.hire_request_id
    JOIN employee e ON e.id = x.employee_id
   WHERE x.tenant_id = p_tenant_id AND x.status = 'ACTIVE' AND x.end_date IS NOT NULL
     AND x.end_date <= DATE_ADD(p_today, INTERVAL GREATEST(IFNULL(p_days, 30), 1) DAY)
     AND (v_admin = 1 OR h.directed_user_id <=> p_actor_id
          OR NOT EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid)
          OR EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid AND m.user_id = p_actor_id))
   ORDER BY x.end_date
   LIMIT 1000;
END $$

-- Para los recordatorios: contratos por terminar del tenant con sus gestores (los del cliente actual y el dirigido) y los
-- administradores, que reciben los de clientes sin gestores.
DROP PROCEDURE IF EXISTS sp_contract_ending $$
CREATE PROCEDURE sp_contract_ending(IN p_tenant_id BIGINT UNSIGNED, IN p_today DATE, IN p_days INT)
BEGIN
  SELECT JSON_OBJECT('requestId', h.uuid, 'hireId', x.uuid,
           'talent', JSON_OBJECT('firstName', e.first_name, 'lastName', e.last_name, 'secondLastName', e.second_last_name),
           'client', x.client_label, 'rq', JSON_UNQUOTE(JSON_EXTRACT(h.doc, '$.requirement.code')), 'position', x.position,
           'startDate', x.start_date, 'endDate', x.end_date,
           'managers', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', u.uuid, 'userId', u.id, 'nameEnc', TO_BASE64(u.full_name_enc),
                                                                'emailEnc', TO_BASE64(u.email_enc), 'locale', u.locale))
                                 FROM app_user u
                                WHERE u.status = 'ACTIVE' AND u.deleted_at IS NULL
                                  AND (u.id = h.directed_user_id
                                       OR u.id IN (SELECT m.user_id FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid))), JSON_ARRAY())) AS doc
    FROM hire x
    JOIN hire_request h ON h.id = x.hire_request_id
    JOIN employee e ON e.id = x.employee_id
   WHERE x.tenant_id = p_tenant_id AND x.status = 'ACTIVE' AND x.end_date IS NOT NULL
     AND x.end_date <= DATE_ADD(p_today, INTERVAL GREATEST(IFNULL(p_days, 30), 1) DAY)
   ORDER BY x.end_date;
  SELECT JSON_OBJECT('id', u.uuid, 'userId', u.id, 'nameEnc', TO_BASE64(u.full_name_enc), 'emailEnc', TO_BASE64(u.email_enc), 'locale', u.locale) AS doc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.status = 'ACTIVE' AND u.deleted_at IS NULL
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.assign');
END $$

DELIMITER ;
