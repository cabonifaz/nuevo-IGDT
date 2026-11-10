-- =====================================================================
-- Firma del usuario para las fichas de ingreso. Solo el propio usuario de un tenant la cambia.
-- =====================================================================
DELIMITER $$

DROP PROCEDURE IF EXISTS sp_user_signature_set $$
CREATE PROCEDURE sp_user_signature_set(IN p_actor_id BIGINT UNSIGNED, IN p_file_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_tenant BIGINT UNSIGNED;
  DECLARE v_file BIGINT UNSIGNED;
  SET v_tenant = (SELECT tenant_id FROM app_user WHERE id = p_actor_id AND deleted_at IS NULL);
  IF v_tenant IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FORBIDDEN'; END IF;
  SELECT id INTO v_file FROM file_object
   WHERE uuid = p_file_uuid AND tenant_id = v_tenant AND owner_type = 'USER_SIGNATURE' AND owner_id = p_actor_id
     AND deleted_at IS NULL;
  IF v_file IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FILE_NOT_FOUND'; END IF;
  INSERT INTO user_signature (user_id, file_id) VALUES (p_actor_id, v_file)
  ON DUPLICATE KEY UPDATE file_id = v_file;
  CALL sp_audit_insert(v_tenant, p_actor_id, NULL, 'SET_SIGNATURE', 'user_signature', p_actor_id, NULL, p_ip);
END $$

DROP PROCEDURE IF EXISTS sp_user_signature_clear $$
CREATE PROCEDURE sp_user_signature_clear(IN p_actor_id BIGINT UNSIGNED, IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_tenant BIGINT UNSIGNED;
  SET v_tenant = (SELECT tenant_id FROM app_user WHERE id = p_actor_id AND deleted_at IS NULL);
  IF v_tenant IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FORBIDDEN'; END IF;
  UPDATE file_object f JOIN user_signature s ON s.file_id = f.id SET f.deleted_at = NOW(3) WHERE s.user_id = p_actor_id;
  DELETE FROM user_signature WHERE user_id = p_actor_id;
  CALL sp_audit_insert(v_tenant, p_actor_id, NULL, 'CLEAR_SIGNATURE', 'user_signature', p_actor_id, NULL, p_ip);
END $$

-- Firma de un usuario del tenant (por uuid) o del propio actor (p_user_uuid NULL): clave de almacenamiento para imprimir.
DROP PROCEDURE IF EXISTS sp_user_signature_get $$
CREATE PROCEDURE sp_user_signature_get(IN p_tenant_id BIGINT UNSIGNED, IN p_actor_id BIGINT UNSIGNED, IN p_user_uuid CHAR(36))
BEGIN
  SELECT f.uuid, f.storage_key, f.mime_type, s.updated_at
    FROM user_signature s
    JOIN app_user u ON u.id = s.user_id AND u.tenant_id = p_tenant_id
    JOIN file_object f ON f.id = s.file_id AND f.deleted_at IS NULL
   WHERE (p_user_uuid IS NULL AND u.id = p_actor_id) OR (p_user_uuid IS NOT NULL AND u.uuid = p_user_uuid);
END $$

DELIMITER ;
