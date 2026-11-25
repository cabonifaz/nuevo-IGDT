-- =====================================================================
-- Fin de contrato (V0021, V0022): alertas de contratos por terminar y operaciones del asistente (renovar, baja, traspaso
-- de área y cambio a otro RQ), bajas programadas, equipo y credenciales del colaborador y eventos para BDT.
-- Gestionan el fin de contrato los mismos que aprueban ingresos: los gestores del cliente del contrato, el gestor
-- dirigido de la solicitud o un administrador (hire.assign); si el cliente no tiene gestores, cualquier gestor con
-- hire.write.
-- =====================================================================
DELIMITER $$

-- El contrato de la solicitud debe estar vigente (ACTIVE), sin una baja programada, y el actor poder gestionarlo.
-- Bloquea la fila del ingreso.
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
  IF EXISTS (SELECT 1 FROM hire_contract_op WHERE hire_id = p_hire_id AND status = 'SCHEDULED') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_SCHEDULED';
  END IF;
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

-- Hace efectiva una baja (op TERMINATE ya registrada y bloqueada por el llamador, dentro de su transacción): el ingreso
-- queda TERMINATED con fecha y motivo de cese, el colaborador cesado si no tiene otro ingreso vigente, su equipo «por
-- recuperar» (si se indicó), sus credenciales activas con baja solicitada y BDT recibe TERMINATED. Si el contrato ya no
-- está vigente, la operación queda anulada. p_actor_id: quien confirmó la baja (también si la ejecuta la rutina diaria).
DROP PROCEDURE IF EXISTS sp_hire_terminate_apply $$
CREATE PROCEDURE sp_hire_terminate_apply(IN p_op_id BIGINT UNSIGNED, IN p_actor_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_hire, v_emp, v_term, v_tenant BIGINT UNSIGNED;
  DECLARE v_date, v_ret DATE;
  DECLARE v_recover TINYINT;
  DECLARE v_op CHAR(36);
  DECLARE v_data JSON;
  DECLARE v_status VARCHAR(12);
  SELECT hire_id, tenant_id, data, uuid INTO v_hire, v_tenant, v_data, v_op FROM hire_contract_op WHERE id = p_op_id;
  SELECT employee_id, status INTO v_emp, v_status FROM hire WHERE id = v_hire FOR UPDATE;
  IF v_status <> 'ACTIVE' THEN
    UPDATE hire_contract_op SET status = 'CANCELLED' WHERE id = p_op_id;
  ELSE
    SET v_date = CAST(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.input.date')) AS DATE);
    SET v_recover = IFNULL(JSON_EXTRACT(v_data, '$.input.recoverEquipment') = CAST('true' AS JSON), 0);
    SET v_ret = IF(v_recover = 1, CAST(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.input.equipmentReturnDate')) AS DATE), NULL);
    SET v_term = (SELECT id FROM master_item WHERE uuid = JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.input.terminationReasonId')));
    UPDATE hire SET status = 'TERMINATED', termination_date = v_date, termination_reason_item_id = v_term, equipment_return_date = v_ret,
           closed_at = NOW(3), closed_by = p_actor_id, updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    UPDATE employee SET status = 'TERMINATED', updated_by = p_actor_id
     WHERE id = v_emp AND NOT EXISTS (SELECT 1 FROM hire WHERE employee_id = v_emp AND status = 'ACTIVE');
    IF v_recover = 1 THEN
      IF NOT EXISTS (SELECT 1 FROM employee_equipment WHERE hire_id = v_hire) THEN CALL sp_employee_equipment_sync(v_hire, p_actor_id); END IF;
      UPDATE employee_equipment SET status = 'PENDING_RETURN', return_due_date = v_ret, updated_by = p_actor_id
       WHERE employee_id = v_emp AND status = 'DELIVERED';
    END IF;
    UPDATE employee_credential SET status = 'DEACTIVATION_REQUESTED', deactivation_requested_at = NOW(3), updated_by = p_actor_id
     WHERE employee_id = v_emp AND status = 'ACTIVE';
    UPDATE hire_contract_op SET status = 'EXECUTED' WHERE id = p_op_id;
    INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
    SELECT v_tenant, 'TERMINATED', h.candidate_uuid, h.requirement_uuid,
           JSON_OBJECT('hireId', x.uuid, 'employeeId', e.uuid, 'talentId', h.talent_uuid, 'operationId', v_op, 'terminationDate', v_date,
                       'reason', (SELECT JSON_OBJECT('id', uuid, 'code', code, 'label', JSON_UNQUOTE(JSON_EXTRACT(label_i18n, '$.es'))) FROM master_item WHERE id = v_term),
                       'recoverEquipment', v_recover = 1, 'equipmentReturnDate', v_ret)
      FROM hire x JOIN hire_request h ON h.id = x.hire_request_id JOIN employee e ON e.id = x.employee_id WHERE x.id = v_hire;
  END IF;
END $$

-- Ejecuta una operación de fin de contrato sobre el ingreso de la solicitud (validada también en la API).
--   RENEW      p_end (> fin actual) y p_reason: extiende el contrato.                                     RENEWED.
--   TERMINATE  p_date (cese), p_term_reason_uuid, p_recover y p_return_date. Si p_date <= p_today se hace efectiva ya
--              (TERMINATED); si es futura queda SCHEDULED y la rutina diaria la aplica en esa fecha.     TERMINATED.
--   TRANSFER   traspaso de área en el mismo cliente: p_area_uuid (distinta de la actual), p_position, p_schedule,
--              p_site_uuid (sede), p_location, p_project, p_end (NULL = indeterminado), p_currency_uuid y p_pay_enc
--              (NULL = sin cambio de remuneración) y p_date (movimiento).                               TRANSFERRED.
--   RQ_CHANGE  p_rqs: uno o más RQ abiertos de BDT y p_date (salida): cierra el contrato (RQ_CHANGED).  RQ_CHANGE_REQUESTED.
-- p_data: entradas, etiquetas y la foto del contrato antes del cambio. p_changes: [{"field","from","to"}].
DROP PROCEDURE IF EXISTS sp_hire_contract_op $$
CREATE PROCEDURE sp_hire_contract_op(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_op_uuid CHAR(36), IN p_type VARCHAR(10), IN p_date DATE, IN p_end DATE, IN p_today DATE, IN p_term_reason_uuid CHAR(36),
  IN p_recover TINYINT, IN p_return_date DATE, IN p_area_uuid CHAR(36), IN p_position VARCHAR(150), IN p_schedule VARCHAR(200),
  IN p_site_uuid CHAR(36), IN p_location VARCHAR(200), IN p_project VARCHAR(200), IN p_currency_uuid CHAR(36), IN p_pay_enc VARBINARY(1024),
  IN p_rqs JSON, IN p_reason VARCHAR(500), IN p_data JSON, IN p_changes JSON, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_hire, v_emp, v_req, v_term, v_area, v_site, v_currency, v_op, v_old_area BIGINT UNSIGNED;
  DECLARE v_start, v_end DATE;
  DECLARE v_change, v_event VARCHAR(20);
  DECLARE v_status VARCHAR(10) DEFAULT 'EXECUTED';
  DECLARE v_event_rq CHAR(36);
  DECLARE v_event_data JSON;
  DECLARE v_scheduled TINYINT DEFAULT 0;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  IF p_type NOT IN ('RENEW', 'TERMINATE', 'TRANSFER', 'RQ_CHANGE') OR p_data IS NULL OR JSON_TYPE(p_data) <> 'OBJECT' OR p_today IS NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  IF p_type = 'TERMINATE' THEN CALL sp_catalog_item_resolve(p_tenant_id, 'TERMINATION_REASON', p_term_reason_uuid, v_term); END IF;
  IF p_type = 'TRANSFER' THEN
    CALL sp_catalog_item_resolve(p_tenant_id, 'AREA', p_area_uuid, v_area);
    IF p_site_uuid IS NOT NULL THEN CALL sp_catalog_item_resolve(p_tenant_id, 'WORK_SITE', p_site_uuid, v_site); END IF;
    IF p_currency_uuid IS NOT NULL THEN CALL sp_catalog_item_resolve(p_tenant_id, 'CURRENCY', p_currency_uuid, v_currency); END IF;
  END IF;
  START TRANSACTION;
  CALL sp_hire_assert_contract(p_actor_id, p_tenant_id, p_request_uuid, v_hire);
  SELECT start_date, end_date, employee_id, hire_request_id, area_item_id INTO v_start, v_end, v_emp, v_req, v_old_area FROM hire WHERE id = v_hire;
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
    SET v_scheduled = p_date > p_today;
    SET v_status = IF(v_scheduled = 1, 'SCHEDULED', 'EXECUTED'), v_change = IF(v_scheduled = 1, 'TERM_SCHEDULED', 'TERMINATED');
  ELSEIF p_type = 'TRANSFER' THEN
    IF TRIM(IFNULL(p_position, '')) = '' OR TRIM(IFNULL(p_schedule, '')) = '' OR v_area = v_old_area THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
    END IF;
    IF p_end IS NOT NULL AND p_end <= p_date THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_DATE_INVALID'; END IF;
    UPDATE hire SET area_item_id = v_area, position = TRIM(p_position), schedule = TRIM(p_schedule), work_site_item_id = v_site,
           location = NULLIF(TRIM(p_location), ''), project_service = NULLIF(TRIM(p_project), ''), end_date = p_end,
           currency_item_id = IFNULL(v_currency, currency_item_id), pay_enc = IFNULL(p_pay_enc, pay_enc),
           updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    -- credenciales: si se pidió modificarlas, quedan con los nuevos datos
    IF JSON_EXTRACT(p_data, '$.input.modifyCredentials') = CAST('true' AS JSON) THEN
      UPDATE employee_credential
         SET username = IFNULL(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(p_data, '$.input.newUsername')), ''), username),
             email = IFNULL(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(p_data, '$.input.newEmail')), ''), email), updated_by = p_actor_id
       WHERE employee_id = v_emp AND status = 'ACTIVE';
    END IF;
    SET v_change = 'TRANSFERRED', v_event = 'TRANSFERRED',
        v_event_data = JSON_OBJECT('date', p_date,
                                   'area', (SELECT JSON_OBJECT('id', uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(label_i18n, '$.es'))) FROM master_item WHERE id = v_area),
                                   'previousArea', (SELECT JSON_OBJECT('id', uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(label_i18n, '$.es'))) FROM master_item WHERE id = v_old_area),
                                   'position', TRIM(p_position), 'endDate', p_end, 'previousEndDate', v_end, 'payChanged', p_pay_enc IS NOT NULL);
  ELSE
    IF p_rqs IS NULL OR JSON_TYPE(p_rqs) <> 'ARRAY' OR JSON_LENGTH(p_rqs) NOT BETWEEN 1 AND 10
       OR (SELECT COUNT(DISTINCT r.id) FROM JSON_TABLE(p_rqs, '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
             JOIN bdt_requirement r ON r.tenant_id = p_tenant_id AND r.uuid = j.u AND r.deleted_at IS NULL AND r.status_group = 'OPEN') <> JSON_LENGTH(p_rqs) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:RQ_NOT_OPEN';
    END IF;
    UPDATE hire SET status = 'RQ_CHANGED', termination_date = p_date, closed_at = NOW(3), closed_by = p_actor_id,
           updated_at = NOW(3), updated_by = p_actor_id
     WHERE id = v_hire;
    SET v_event_rq = JSON_UNQUOTE(JSON_EXTRACT(p_rqs, '$[0]'));
    SET v_change = 'RQ_CHANGED', v_event = 'RQ_CHANGE_REQUESTED', v_status = 'REQUESTED',
        v_event_data = (SELECT JSON_OBJECT('requirementCode', r.code, 'requirementTitle', r.title,
                                           'client', JSON_OBJECT('id', r.client_uuid, 'label', r.client_label), 'date', p_date,
                                           'fromRequirementId', h.requirement_uuid,
                                           'requirements', (SELECT JSON_ARRAYAGG(JSON_OBJECT('id', q.uuid, 'code', q.code, 'title', q.title,
                                                                                             'client', JSON_OBJECT('id', q.client_uuid, 'label', q.client_label)))
                                                              FROM (SELECT r2.uuid, r2.code, r2.title, r2.client_uuid, r2.client_label
                                                                      FROM JSON_TABLE(p_rqs, '$[*]' COLUMNS (n FOR ORDINALITY, u CHAR(36) PATH '$')) j
                                                                      JOIN bdt_requirement r2 ON r2.tenant_id = p_tenant_id AND r2.uuid = j.u
                                                                     ORDER BY j.n) q))
                          FROM bdt_requirement r JOIN hire_request h ON h.id = v_req
                         WHERE r.tenant_id = p_tenant_id AND r.uuid = v_event_rq);
  END IF;
  INSERT INTO hire_contract_op (uuid, tenant_id, hire_id, type, status, effective_date, data, reason, created_by)
  VALUES (p_op_uuid, p_tenant_id, v_hire, p_type, v_status, IFNULL(IF(p_type = 'RENEW', p_end, p_date), p_today), p_data,
          NULLIF(TRIM(p_reason), ''), p_actor_id);
  SET v_op = LAST_INSERT_ID();
  INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
  VALUES (p_tenant_id, v_hire, v_change, IFNULL(p_changes, JSON_ARRAY()), NULLIF(TRIM(p_reason), ''), p_actor_id);
  IF p_type = 'TERMINATE' THEN
    IF v_scheduled = 0 THEN CALL sp_hire_terminate_apply(v_op, p_actor_id); END IF;
  ELSE
    INSERT INTO hire_event (tenant_id, type, candidate_uuid, requirement_uuid, data)
    SELECT p_tenant_id, v_event, h.candidate_uuid, IFNULL(v_event_rq, h.requirement_uuid),
           JSON_MERGE_PATCH(JSON_OBJECT('hireId', x.uuid, 'employeeId', e.uuid, 'talentId', h.talent_uuid, 'operationId', p_op_uuid), v_event_data)
      FROM hire x JOIN hire_request h ON h.id = x.hire_request_id JOIN employee e ON e.id = x.employee_id WHERE x.id = v_hire;
  END IF;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, p_type, 'hire_request', p_request_uuid, JSON_OBJECT('operation', p_op_uuid, 'status', v_status), p_ip);
  SELECT p_op_uuid AS uuid, v_status AS status;
END $$

-- Cambia la fecha (RESCHEDULE) o anula (CANCEL) una baja programada, con motivo. Si la nueva fecha ya llegó, la baja se
-- hace efectiva al momento. Devuelve el estado final de la operación.
DROP PROCEDURE IF EXISTS sp_hire_contract_op_change $$
CREATE PROCEDURE sp_hire_contract_op_change(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36),
  IN p_op_uuid CHAR(36), IN p_action VARCHAR(12), IN p_date DATE, IN p_reason VARCHAR(500), IN p_today DATE, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_op, v_hire BIGINT UNSIGNED;
  DECLARE v_old, v_start DATE;
  DECLARE v_status VARCHAR(10);
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  CALL sp_hire_contract_assert_manager(p_actor_id, p_tenant_id, p_request_uuid);
  IF p_action NOT IN ('RESCHEDULE', 'CANCEL') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  IF TRIM(IFNULL(p_reason, '')) = '' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:REASON_REQUIRED'; END IF;
  START TRANSACTION;
  SELECT o.id, o.hire_id, o.effective_date, o.status, x.start_date INTO v_op, v_hire, v_old, v_status, v_start
    FROM hire_contract_op o JOIN hire x ON x.id = o.hire_id JOIN hire_request h ON h.id = x.hire_request_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid AND o.uuid = p_op_uuid FOR UPDATE;
  IF v_op IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status <> 'SCHEDULED' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_NOT_SCHEDULED'; END IF;
  IF p_action = 'CANCEL' THEN
    UPDATE hire_contract_op SET status = 'CANCELLED', updated_by = p_actor_id WHERE id = v_op;
    INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
    VALUES (p_tenant_id, v_hire, 'TERM_CANCELLED', JSON_ARRAY(JSON_OBJECT('field', 'terminationDate', 'from', v_old)), TRIM(p_reason), p_actor_id);
    SET v_status = 'CANCELLED';
  ELSE
    IF p_date IS NULL OR p_date < v_start OR p_date = v_old THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CONTRACT_DATE_INVALID'; END IF;
    UPDATE hire_contract_op SET effective_date = p_date, data = JSON_SET(data, '$.input.date', DATE_FORMAT(p_date, '%Y-%m-%d')),
           updated_by = p_actor_id
     WHERE id = v_op;
    INSERT INTO hire_change (tenant_id, hire_id, type, changes, reason, created_by)
    VALUES (p_tenant_id, v_hire, 'TERM_RESCHEDULED', JSON_ARRAY(JSON_OBJECT('field', 'terminationDate', 'from', v_old, 'to', p_date)),
            TRIM(p_reason), p_actor_id);
    IF p_date <= p_today THEN CALL sp_hire_terminate_apply(v_op, p_actor_id); END IF;
    SET v_status = (SELECT status FROM hire_contract_op WHERE id = v_op);
  END IF;
  COMMIT;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, CONCAT('TERMINATE_', p_action), 'hire_request', p_request_uuid,
                       JSON_OBJECT('operation', p_op_uuid, 'date', p_date), p_ip);
  SELECT v_status AS status;
END $$

-- Rutina diaria: hace efectivas las bajas programadas cuya fecha de cese ya llegó (todas las empresas). Cada una en su
-- transacción; varias instancias no chocan (FOR UPDATE SKIP LOCKED y el estado SCHEDULED). Devuelve cuántas aplicó.
DROP PROCEDURE IF EXISTS sp_contract_apply_scheduled $$
CREATE PROCEDURE sp_contract_apply_scheduled(IN p_today DATE)
BEGIN
  DECLARE v_id, v_actor, v_tenant BIGINT UNSIGNED;
  DECLARE v_uuid CHAR(36);
  DECLARE v_n INT DEFAULT 0;
  DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN ROLLBACK; RESIGNAL; END;
  apply_loop: LOOP
    START TRANSACTION;
    SET v_id = NULL;
    SELECT id, IFNULL(updated_by, created_by), tenant_id, uuid INTO v_id, v_actor, v_tenant, v_uuid
      FROM hire_contract_op
     WHERE status = 'SCHEDULED' AND type = 'TERMINATE' AND effective_date <= p_today
     ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED;
    IF v_id IS NULL THEN
      COMMIT;
      LEAVE apply_loop;
    END IF;
    CALL sp_hire_terminate_apply(v_id, v_actor);
    COMMIT;
    CALL sp_audit_insert(v_tenant, v_actor, NULL, 'TERMINATE_EFFECTIVE', 'hire_contract_op', v_uuid, JSON_OBJECT('date', p_today), NULL);
    SET v_n = v_n + 1;
    IF v_n >= 500 THEN LEAVE apply_loop; END IF;
  END LOOP;
  SELECT v_n AS applied;
END $$

-- Contexto del fin de contrato de una solicitud: contrato vigente (remuneración cifrada en base64 para la API), si el
-- usuario puede gestionarlo, operaciones (con sus envíos y quién firma), equipo y credenciales del colaborador.
DROP PROCEDURE IF EXISTS sp_hire_contract_get $$
CREATE PROCEDURE sp_hire_contract_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36))
BEGIN
  DECLARE v_admin, v_write TINYINT;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SET v_admin = EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.assign');
  SET v_write = EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.write');
  SELECT JSON_OBJECT('hireId', x.uuid, 'requestId', h.uuid, 'employeeId', e.uuid, 'status', x.status,
           'talent', JSON_OBJECT('firstName', e.first_name, 'lastName', e.last_name, 'secondLastName', e.second_last_name),
           'client', JSON_OBJECT('id', x.client_uuid, 'label', x.client_label), 'requirement', JSON_EXTRACT(h.doc, '$.requirement.code'),
           'requirementId', h.requirement_uuid,
           'position', x.position, 'area', JSON_OBJECT('id', a.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(a.label_i18n, '$.es'))),
           'site', IF(s.id IS NULL, NULL, JSON_OBJECT('id', s.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(s.label_i18n, '$.es')))),
           'location', x.location, 'projectService', x.project_service,
           'contract', JSON_OBJECT('id', c.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(c.label_i18n, '$.es')), 'group', c.str1),
           'currency', JSON_OBJECT('id', cu.uuid, 'code', cu.code), 'payEnc', TO_BASE64(x.pay_enc),
           'schedule', x.schedule, 'startDate', x.start_date, 'endDate', x.end_date,
           'terminationDate', x.termination_date, 'equipmentReturnDate', x.equipment_return_date,
           'terminationReason', IF(tr.id IS NULL, NULL, JSON_OBJECT('id', tr.uuid, 'label', JSON_UNQUOTE(JSON_EXTRACT(tr.label_i18n, '$.es')))),
           'canResend', v_write = 1
                        AND (v_admin = 1 OR h.directed_user_id <=> p_actor_id
                             OR NOT EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid)
                             OR EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid AND m.user_id = p_actor_id)),
           'canManage', x.status = 'ACTIVE' AND v_write = 1
                        AND NOT EXISTS (SELECT 1 FROM hire_contract_op so WHERE so.hire_id = x.id AND so.status = 'SCHEDULED')
                        AND (v_admin = 1 OR h.directed_user_id <=> p_actor_id
                             OR NOT EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid)
                             OR EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid AND m.user_id = p_actor_id)),
           'scheduled', (SELECT JSON_OBJECT('id', so.uuid, 'type', so.type, 'effectiveDate', so.effective_date)
                           FROM hire_contract_op so WHERE so.hire_id = x.id AND so.status = 'SCHEDULED' LIMIT 1),
           'users', x.user_request, 'equipmentRequest', x.equipment_request,
           'credential', (SELECT JSON_OBJECT('id', cr.uuid, 'username', cr.username, 'email', cr.email, 'status', cr.status, 'source', cr.source)
                            FROM employee_credential cr WHERE cr.employee_id = e.id ORDER BY cr.status = 'ACTIVE' DESC, cr.id DESC LIMIT 1),
           'operations', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', o.uuid, 'type', o.type, 'status', o.status, 'effectiveDate', o.effective_date,
                                         'data', JSON_REMOVE(o.data, '$.payEnc', '$.beforePayEnc'), 'reason', o.reason, 'at', o.created_at,
                                         'byNameEnc', TO_BASE64(ou.full_name_enc), 'updatedAt', o.updated_at, 'updatedByNameEnc', TO_BASE64(uu.full_name_enc),
                                         'dispatches', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('form', d.form, 'reason', d.reason, 'recipients', d.recipients,
                                                                     'status', d.status, 'error', d.error, 'at', d.created_at))
                                                                 FROM hire_form_dispatch d WHERE d.contract_op_id = o.id), JSON_ARRAY())))
                                  FROM (SELECT * FROM hire_contract_op WHERE hire_id = x.id ORDER BY id DESC LIMIT 50) o
                                  LEFT JOIN app_user ou ON ou.id = o.created_by
                                  LEFT JOIN app_user uu ON uu.id = o.updated_by), JSON_ARRAY()),
           'equipment', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', q.uuid, 'label', q.label, 'details', q.details, 'status', q.status,
                                        'deliveredAt', q.delivered_at, 'returnDueDate', q.return_due_date, 'returnedAt', q.returned_at))
                                 FROM employee_equipment q WHERE q.employee_id = e.id AND q.status <> 'RETURNED'), JSON_ARRAY())) AS doc
    FROM hire_request h
    JOIN hire x ON x.id = h.hire_id
    JOIN employee e ON e.id = x.employee_id
    JOIN master_item a ON a.id = x.area_item_id
    JOIN master_item c ON c.id = x.contract_modality_item_id
    JOIN master_item cu ON cu.id = x.currency_item_id
    LEFT JOIN master_item s ON s.id = x.work_site_item_id
    LEFT JOIN master_item tr ON tr.id = x.termination_reason_item_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid;
END $$

-- Operación registrada (para reenviar sus documentos), con quién firma: quien la confirmó o le cambió la fecha.
DROP PROCEDURE IF EXISTS sp_hire_contract_op_get $$
CREATE PROCEDURE sp_hire_contract_op_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_request_uuid CHAR(36), IN p_op_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('id', o.uuid, 'type', o.type, 'status', o.status, 'effectiveDate', o.effective_date, 'data', o.data, 'reason', o.reason,
           'at', o.created_at, 'signerId', IFNULL(uu.uuid, cu.uuid), 'signerNameEnc', TO_BASE64(IFNULL(uu.full_name_enc, cu.full_name_enc)),
           'signerEmailEnc', TO_BASE64(IFNULL(uu.email_enc, cu.email_enc))) AS doc
    FROM hire_contract_op o JOIN hire x ON x.id = o.hire_id JOIN hire_request h ON h.id = x.hire_request_id
    LEFT JOIN app_user cu ON cu.id = o.created_by
    LEFT JOIN app_user uu ON uu.id = o.updated_by
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_request_uuid AND o.uuid = p_op_uuid;
END $$

-- Para reenviar o cambiar una baja programada: el actor debe poder gestionar el contrato (vigente o ya cerrado).
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

-- Contratos vigentes que terminan hasta p_today + p_days (o ya vencidos) que el usuario puede gestionar. Los que tienen
-- una baja programada ya no piden acción.
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
     AND NOT EXISTS (SELECT 1 FROM hire_contract_op so WHERE so.hire_id = x.id AND so.status = 'SCHEDULED')
     AND (v_admin = 1 OR h.directed_user_id <=> p_actor_id
          OR NOT EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid)
          OR EXISTS (SELECT 1 FROM client_manager m WHERE m.tenant_id = x.tenant_id AND m.client_uuid = x.client_uuid AND m.user_id = p_actor_id))
   ORDER BY x.end_date
   LIMIT 1000;
END $$

-- Para los recordatorios: contratos por terminar del tenant (sin baja programada) con sus gestores (los del cliente y el
-- dirigido) y los administradores, que reciben los de clientes sin gestores.
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
     AND NOT EXISTS (SELECT 1 FROM hire_contract_op so WHERE so.hire_id = x.id AND so.status = 'SCHEDULED')
   ORDER BY x.end_date;
  SELECT JSON_OBJECT('id', u.uuid, 'userId', u.id, 'nameEnc', TO_BASE64(u.full_name_enc), 'emailEnc', TO_BASE64(u.email_enc), 'locale', u.locale) AS doc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.status = 'ACTIVE' AND u.deleted_at IS NULL
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.assign');
END $$

DELIMITER ;
