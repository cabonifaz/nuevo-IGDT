-- =====================================================================
-- Formato de las fichas de ingreso: colores, logo y datos de cada formato. Lo cambian el administrador del tenant
-- (tenant.branding.manage) y el Super Admin (platform.tenants.manage) sobre cualquier tenant.
-- =====================================================================
DELIMITER $$

DROP PROCEDURE IF EXISTS sp_hire_form_style_assert $$
CREATE PROCEDURE sp_hire_form_style_assert(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  DECLARE v_actor_tenant BIGINT UNSIGNED;
  IF p_actor_id IS NULL OR p_tenant_id IS NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FORBIDDEN';
  END IF;
  SET v_actor_tenant = (SELECT tenant_id FROM app_user WHERE id = p_actor_id);
  IF v_actor_tenant IS NULL THEN
    IF NOT EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'platform.tenants.manage') THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FORBIDDEN';
    END IF;
  ELSE
    IF v_actor_tenant <> p_tenant_id THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:TENANT_MISMATCH';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM v_user_permission WHERE user_id = p_actor_id AND code = 'tenant.branding.manage') THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FORBIDDEN';
    END IF;
  END IF;
END $$

-- Formato vigente: estilo guardado, logo propio de las fichas y logo de la marca (claves de almacenamiento solo para
-- imprimir; la app no las expone), empresa y RUC.
DROP PROCEDURE IF EXISTS sp_hire_form_style_get $$
CREATE PROCEDURE sp_hire_form_style_get(IN p_tenant_id BIGINT UNSIGNED)
BEGIN
  SELECT JSON_OBJECT(
           'style', s.style, 'updatedAt', s.updated_at,
           'logo', IF(lf.id IS NULL, NULL, JSON_OBJECT('id', lf.uuid, 'key', lf.storage_key, 'mime', lf.mime_type, 'name', lf.original_name)),
           'brandLogo', IF(bf.id IS NULL, NULL, JSON_OBJECT('id', bf.uuid, 'key', bf.storage_key, 'mime', bf.mime_type)),
           'company', IFNULL(t.legal_name, t.name), 'displayName', IFNULL(b.display_name, t.name), 'taxId', t.tax_id) AS doc
    FROM tenant t
    LEFT JOIN hire_form_style s ON s.tenant_id = t.id
    LEFT JOIN file_object lf ON lf.id = s.logo_file_id AND lf.deleted_at IS NULL
    LEFT JOIN tenant_branding b ON b.tenant_id = t.id
    LEFT JOIN file_object bf ON bf.id = b.logo_file_id AND bf.deleted_at IS NULL
   WHERE t.id = p_tenant_id AND t.deleted_at IS NULL;
END $$

DROP PROCEDURE IF EXISTS sp_hire_form_style_save $$
CREATE PROCEDURE sp_hire_form_style_save(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_style JSON,
  IN p_clear_logo TINYINT, IN p_ip VARCHAR(45))
BEGIN
  CALL sp_hire_form_style_assert(p_actor_id, p_tenant_id);
  IF p_style IS NOT NULL AND JSON_TYPE(p_style) <> 'OBJECT' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  INSERT INTO hire_form_style (tenant_id, style, updated_by) VALUES (p_tenant_id, p_style, p_actor_id)
  ON DUPLICATE KEY UPDATE style = p_style, logo_file_id = IF(p_clear_logo = 1, NULL, logo_file_id), updated_by = p_actor_id;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPDATE', 'hire_form_style', p_tenant_id,
       JSON_OBJECT('style', p_style, 'clearLogo', p_clear_logo), p_ip);
END $$

DROP PROCEDURE IF EXISTS sp_hire_form_logo_set $$
CREATE PROCEDURE sp_hire_form_logo_set(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_file_uuid CHAR(36),
  IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_file_id BIGINT UNSIGNED;
  CALL sp_hire_form_style_assert(p_actor_id, p_tenant_id);
  SELECT id INTO v_file_id FROM file_object
   WHERE uuid = p_file_uuid AND tenant_id = p_tenant_id AND owner_type = 'TENANT_BRANDING' AND category = 'FORM_LOGO'
     AND deleted_at IS NULL;
  IF v_file_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:FILE_NOT_FOUND'; END IF;
  INSERT INTO hire_form_style (tenant_id, logo_file_id, updated_by) VALUES (p_tenant_id, v_file_id, p_actor_id)
  ON DUPLICATE KEY UPDATE logo_file_id = v_file_id, updated_by = p_actor_id;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'SET_FILE', 'hire_form_style', p_tenant_id,
       JSON_OBJECT('kind', 'FORM_LOGO', 'file', p_file_uuid), p_ip);
END $$

DELIMITER ;
