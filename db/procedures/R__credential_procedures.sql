-- =====================================================================
-- Credenciales del colaborador (V0022): usuario de red y correo creados con la ficha de usuarios (FT-GS-01).
-- ACTIVE al aprobar la ficha de usuarios del ingreso; DEACTIVATION_REQUESTED cuando la baja se hace efectiva (o si se
-- revoca el ingreso que las creó); DEACTIVATED lo marcará Soporte (pantallas futuras).
-- =====================================================================
DELIMITER $$

-- Registra (o actualiza) las credenciales de la ficha de usuarios aprobada de la solicitud, si el colaborador ya existe
-- (el talento ya tuvo un ingreso o se acaba de registrar la FMI). Si ya tiene otras credenciales activas no crea otras.
DROP PROCEDURE IF EXISTS sp_employee_credential_sync $$
CREATE PROCEDURE sp_employee_credential_sync(IN p_request_id BIGINT UNSIGNED, IN p_actor_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_tenant, v_emp, v_own BIGINT UNSIGNED;
  DECLARE v_data JSON;
  SELECT h.tenant_id, e.id INTO v_tenant, v_emp
    FROM hire_request h JOIN employee e ON e.tenant_id = h.tenant_id AND e.talent_uuid = h.talent_uuid
   WHERE h.id = p_request_id;
  SET v_data = (SELECT JSON_EXTRACT(a.data, '$.users') FROM hire_form_approval a WHERE a.hire_request_id = p_request_id AND a.form = 'USERS');
  IF v_emp IS NOT NULL AND v_data IS NOT NULL AND JSON_TYPE(v_data) = 'OBJECT' THEN
    SET v_own = (SELECT id FROM employee_credential WHERE hire_request_id = p_request_id ORDER BY id DESC LIMIT 1);
    IF v_own IS NOT NULL THEN
      UPDATE employee_credential SET username = NULLIF(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.username')), ''),
             email = NULLIF(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.email')), ''), updated_by = NULLIF(p_actor_id, 0)
       WHERE id = v_own;
    ELSEIF NOT EXISTS (SELECT 1 FROM employee_credential WHERE employee_id = v_emp AND status = 'ACTIVE') THEN
      INSERT INTO employee_credential (uuid, tenant_id, employee_id, hire_request_id, username, email, status, source, created_by)
      VALUES (UUID(), v_tenant, v_emp, p_request_id, NULLIF(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.username')), ''),
              NULLIF(JSON_UNQUOTE(JSON_EXTRACT(v_data, '$.email')), ''), 'ACTIVE', 'USERS_FORM', NULLIF(p_actor_id, 0));
    END IF;
  END IF;
END $$

DELIMITER ;
