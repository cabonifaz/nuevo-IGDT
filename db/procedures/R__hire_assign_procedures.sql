-- =====================================================================
-- Asignación de las solicitudes de ingreso: solo el gestor asignado las trabaja; el admin las reasigna.
-- =====================================================================
DELIMITER $$

-- La solicitud debe estar en uno de p_states (lista separada por comas) y asignada al actor.
DROP PROCEDURE IF EXISTS sp_hire_assert_assignee $$
CREATE PROCEDURE sp_hire_assert_assignee(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36),
  IN p_states VARCHAR(60))
BEGIN
  DECLARE v_status VARCHAR(12);
  DECLARE v_user BIGINT UNSIGNED;
  SELECT status, assigned_user_id INTO v_status, v_user FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
  IF v_status IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status = 'PENDING' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_NOT_TAKEN'; END IF;
  IF FIND_IN_SET(v_status, p_states) = 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE'; END IF;
  IF v_user IS NULL OR v_user <> p_actor_id THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_NOT_ASSIGNED'; END IF;
END $$

-- Tomar: una pendiente la toma cualquier gestor; si ya la tiene otro, solo quien puede reasignar (hire.assign).
DROP PROCEDURE IF EXISTS sp_hire_request_take $$
CREATE PROCEDURE sp_hire_request_take(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_status VARCHAR(12);
  DECLARE v_user BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.write');
  SELECT status, assigned_user_id INTO v_status, v_user FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
  IF v_status IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status NOT IN ('PENDING', 'IN_PROGRESS') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE'; END IF;
  IF v_user IS NOT NULL AND v_user <> p_actor_id
     AND NOT EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'hire.assign') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_NOT_ASSIGNED';
  END IF;
  UPDATE hire_request SET status = 'IN_PROGRESS', assigned_user_id = p_actor_id, taken_at = NOW(3)
   WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'TAKE', 'hire_request', p_uuid,
       IF(v_user IS NOT NULL AND v_user <> p_actor_id, JSON_OBJECT('from', v_user), NULL), p_ip);
END $$

-- Reasignar (admin): a un gestor activo del tenant con hire.write. Vale para solicitudes en proceso o ya confirmadas
-- (quien corrige o revoca el ingreso).
DROP PROCEDURE IF EXISTS sp_hire_request_assign $$
CREATE PROCEDURE sp_hire_request_assign(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36),
  IN p_user_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_status VARCHAR(12);
  DECLARE v_from BIGINT UNSIGNED;
  DECLARE v_to BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.assign');
  SELECT status, assigned_user_id INTO v_status, v_from FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
  IF v_status IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF v_status NOT IN ('PENDING', 'IN_PROGRESS', 'COMPLETED') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:HIRE_STATE'; END IF;
  SELECT u.id INTO v_to FROM app_user u
   WHERE u.uuid = p_user_uuid AND u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
  IF v_to IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  UPDATE hire_request SET assigned_user_id = v_to, status = IF(status = 'PENDING', 'IN_PROGRESS', status),
         taken_at = IFNULL(taken_at, NOW(3))
   WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'ASSIGN', 'hire_request', p_uuid, JSON_OBJECT('from', v_from, 'to', v_to), p_ip);
END $$

-- Gestores a los que se puede asignar (activos con hire.write).
DROP PROCEDURE IF EXISTS sp_hire_assignees $$
CREATE PROCEDURE sp_hire_assignees(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.assign');
  SELECT JSON_OBJECT('id', u.uuid, 'nameEnc', TO_BASE64(u.full_name_enc)) AS doc
    FROM app_user u
   WHERE u.tenant_id = p_tenant_id AND u.deleted_at IS NULL AND u.status = 'ACTIVE'
     AND EXISTS (SELECT 1 FROM v_user_permission vp WHERE vp.user_id = u.id AND vp.code = 'hire.write');
END $$

DELIMITER ;
