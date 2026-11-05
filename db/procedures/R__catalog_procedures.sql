-- =====================================================================
-- Catálogos maestros que IGDT gobierna y comparte con BDT: contactos de cliente, tarifario y el historial de cambios
-- que BDT lee con su token de integración (los ítems de maestro se guardan con sp_master_item_upsert).
-- Todo cambio de un catálogo compartido deja una fila en catalog_change.
-- =====================================================================
DELIMITER $$

-- Cliente del tenant (ítem activo del maestro CLIENT)
DROP PROCEDURE IF EXISTS sp_catalog_client_resolve $$
CREATE PROCEDURE sp_catalog_client_resolve(IN p_tenant_id BIGINT UNSIGNED, IN p_client_uuid CHAR(36), OUT o_id BIGINT UNSIGNED)
BEGIN
  SET o_id = NULL;
  SELECT i.id INTO o_id FROM master_item i JOIN master_table t ON t.id = i.master_table_id AND t.code = 'CLIENT'
   WHERE i.uuid = p_client_uuid AND i.tenant_id = p_tenant_id AND i.deleted_at IS NULL;
  IF o_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:CLIENT_NOT_FOUND'; END IF;
END $$

-- Ítem de un maestro (global o del tenant) por uuid
DROP PROCEDURE IF EXISTS sp_catalog_item_resolve $$
CREATE PROCEDURE sp_catalog_item_resolve(IN p_tenant_id BIGINT UNSIGNED, IN p_table VARCHAR(60), IN p_uuid CHAR(36), OUT o_id BIGINT UNSIGNED)
BEGIN
  SET o_id = NULL;
  SELECT i.id INTO o_id FROM master_item i JOIN master_table t ON t.id = i.master_table_id AND t.code = p_table
   WHERE i.uuid = p_uuid AND i.tenant_key IN (0, p_tenant_id) AND i.deleted_at IS NULL AND i.is_active = 1;
  IF o_id IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
END $$

-- ---------------- Contactos de cliente ----------------

DROP PROCEDURE IF EXISTS sp_client_contact_list $$
CREATE PROCEDURE sp_client_contact_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_client_uuid CHAR(36))
BEGIN
  DECLARE v_client BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  CALL sp_catalog_client_resolve(p_tenant_id, p_client_uuid, v_client);
  SELECT JSON_OBJECT('id', cc.uuid, 'clientId', p_client_uuid, 'firstName', cc.first_name, 'lastName', cc.last_name,
           'secondLastName', cc.second_last_name, 'position', cc.position, 'phone', cc.phone, 'phone2', cc.phone2,
           'email', cc.email, 'email2', cc.email2) AS doc
    FROM client_contact cc
   WHERE cc.tenant_id = p_tenant_id AND cc.client_item_id = v_client AND cc.deleted_at IS NULL
   ORDER BY cc.last_name, cc.first_name;
END $$

DROP PROCEDURE IF EXISTS sp_client_contact_save $$
CREATE PROCEDURE sp_client_contact_save(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36),
  IN p_client_uuid CHAR(36), IN p_first VARCHAR(100), IN p_last VARCHAR(100), IN p_second VARCHAR(100), IN p_position VARCHAR(150),
  IN p_phone VARCHAR(30), IN p_phone2 VARCHAR(30), IN p_email VARCHAR(254), IN p_email2 VARCHAR(254), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_client, v_id BIGINT UNSIGNED;
  DECLARE v_owner BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'catalog.manage');
  CALL sp_catalog_client_resolve(p_tenant_id, p_client_uuid, v_client);
  IF TRIM(IFNULL(p_first, '')) = '' OR TRIM(IFNULL(p_last, '')) = '' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION'; END IF;
  SELECT id, tenant_id INTO v_id, v_owner FROM client_contact WHERE uuid = p_uuid;
  IF v_id IS NULL THEN
    INSERT INTO client_contact (uuid, tenant_id, client_item_id, first_name, last_name, second_last_name, position, phone, phone2,
                                email, email2, created_by)
    VALUES (p_uuid, p_tenant_id, v_client, TRIM(p_first), TRIM(p_last), NULLIF(TRIM(p_second), ''), NULLIF(TRIM(p_position), ''),
            NULLIF(TRIM(p_phone), ''), NULLIF(TRIM(p_phone2), ''), NULLIF(LOWER(TRIM(p_email)), ''), NULLIF(LOWER(TRIM(p_email2)), ''), p_actor_id);
  ELSE
    IF v_owner <> p_tenant_id THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
    UPDATE client_contact SET client_item_id = v_client, first_name = TRIM(p_first), last_name = TRIM(p_last),
           second_last_name = NULLIF(TRIM(p_second), ''), position = NULLIF(TRIM(p_position), ''), phone = NULLIF(TRIM(p_phone), ''),
           phone2 = NULLIF(TRIM(p_phone2), ''), email = NULLIF(LOWER(TRIM(p_email)), ''), email2 = NULLIF(LOWER(TRIM(p_email2)), ''),
           deleted_at = NULL, deleted_by = NULL, updated_by = p_actor_id
     WHERE id = v_id;
  END IF;
  INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op) VALUES (p_tenant_id, 'CLIENT_CONTACT', p_uuid, 'UPSERT');
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPSERT', 'client_contact', p_uuid, JSON_OBJECT('client', p_client_uuid), p_ip);
END $$

DROP PROCEDURE IF EXISTS sp_client_contact_delete $$
CREATE PROCEDURE sp_client_contact_delete(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'catalog.manage');
  UPDATE client_contact SET deleted_at = NOW(3), deleted_by = p_actor_id
   WHERE uuid = p_uuid AND tenant_id = p_tenant_id AND deleted_at IS NULL;
  IF ROW_COUNT() = 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op) VALUES (p_tenant_id, 'CLIENT_CONTACT', p_uuid, 'DELETE');
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'DELETE', 'client_contact', p_uuid, NULL, p_ip);
END $$

-- ---------------- Tarifario ----------------

DROP PROCEDURE IF EXISTS sp_tariff_list $$
CREATE PROCEDURE sp_tariff_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_client_uuid CHAR(36), IN p_profile_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'catalog.manage');
  SELECT JSON_OBJECT('id', t.uuid, 'client', IF(c.id IS NULL, NULL, JSON_OBJECT('id', c.uuid, 'label', c.label_i18n)),
           'profile', JSON_OBJECT('id', p.uuid, 'label', p.label_i18n), 'currency', JSON_OBJECT('id', cu.uuid, 'code', cu.code),
           'rateType', JSON_OBJECT('id', rt.uuid, 'code', rt.code, 'label', rt.label_i18n), 'rate', t.rate,
           'validFrom', t.valid_from, 'validTo', t.valid_to, 'notes', t.notes, 'updatedAt', IFNULL(t.updated_at, t.created_at)) AS doc
    FROM tariff t
    JOIN master_item p ON p.id = t.profile_item_id
    JOIN master_item cu ON cu.id = t.currency_item_id
    JOIN master_item rt ON rt.id = t.rate_type_item_id
    LEFT JOIN master_item c ON c.id = t.client_item_id
   WHERE t.tenant_id = p_tenant_id AND t.deleted_at IS NULL
     AND (p_client_uuid IS NULL OR c.uuid = p_client_uuid)
     AND (p_profile_uuid IS NULL OR p.uuid = p_profile_uuid)
   ORDER BY c.id IS NOT NULL, JSON_UNQUOTE(JSON_EXTRACT(c.label_i18n, '$.es')), JSON_UNQUOTE(JSON_EXTRACT(p.label_i18n, '$.es')), t.valid_from DESC;
END $$

-- Una tarifa no puede cruzar su vigencia con otra del mismo cliente (o general), perfil y tipo de tarifa.
DROP PROCEDURE IF EXISTS sp_tariff_save $$
CREATE PROCEDURE sp_tariff_save(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36),
  IN p_client_uuid CHAR(36), IN p_profile_uuid CHAR(36), IN p_currency_uuid CHAR(36), IN p_rate_type_uuid CHAR(36),
  IN p_rate DECIMAL(12,2), IN p_from DATE, IN p_to DATE, IN p_notes VARCHAR(300), IN p_ip VARCHAR(45))
BEGIN
  DECLARE v_client, v_profile, v_currency, v_rate_type, v_id, v_owner BIGINT UNSIGNED;
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'catalog.manage');
  IF p_client_uuid IS NOT NULL THEN CALL sp_catalog_client_resolve(p_tenant_id, p_client_uuid, v_client); END IF;
  CALL sp_catalog_item_resolve(p_tenant_id, 'RQ_PROFILE', p_profile_uuid, v_profile);
  CALL sp_catalog_item_resolve(p_tenant_id, 'CURRENCY', p_currency_uuid, v_currency);
  CALL sp_catalog_item_resolve(p_tenant_id, 'RATE_TYPE', p_rate_type_uuid, v_rate_type);
  IF p_rate IS NULL OR p_rate <= 0 OR p_from IS NULL OR (p_to IS NOT NULL AND p_to < p_from) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  SELECT id, tenant_id INTO v_id, v_owner FROM tariff WHERE uuid = p_uuid;
  IF v_id IS NOT NULL AND v_owner <> p_tenant_id THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  IF EXISTS (SELECT 1 FROM tariff t
              WHERE t.tenant_id = p_tenant_id AND t.deleted_at IS NULL AND t.uuid <> p_uuid
                AND t.profile_item_id = v_profile AND t.rate_type_item_id = v_rate_type AND t.client_item_id <=> v_client
                AND t.valid_from <= IFNULL(p_to, '9999-12-31') AND IFNULL(t.valid_to, '9999-12-31') >= p_from) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:TARIFF_OVERLAP';
  END IF;
  IF v_id IS NULL THEN
    INSERT INTO tariff (uuid, tenant_id, client_item_id, profile_item_id, currency_item_id, rate_type_item_id, rate, valid_from, valid_to, notes, created_by)
    VALUES (p_uuid, p_tenant_id, v_client, v_profile, v_currency, v_rate_type, p_rate, p_from, p_to, NULLIF(TRIM(p_notes), ''), p_actor_id);
  ELSE
    UPDATE tariff SET client_item_id = v_client, profile_item_id = v_profile, currency_item_id = v_currency, rate_type_item_id = v_rate_type,
           rate = p_rate, valid_from = p_from, valid_to = p_to, notes = NULLIF(TRIM(p_notes), ''), updated_by = p_actor_id
     WHERE id = v_id;
  END IF;
  INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op) VALUES (p_tenant_id, 'TARIFF', p_uuid, 'UPSERT');
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'UPSERT', 'tariff', p_uuid,
    JSON_OBJECT('client', p_client_uuid, 'profile', p_profile_uuid, 'rate', p_rate, 'from', p_from, 'to', p_to), p_ip);
END $$

DROP PROCEDURE IF EXISTS sp_tariff_delete $$
CREATE PROCEDURE sp_tariff_delete(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'catalog.manage');
  UPDATE tariff SET deleted_at = NOW(3), deleted_by = p_actor_id WHERE uuid = p_uuid AND tenant_id = p_tenant_id AND deleted_at IS NULL;
  IF ROW_COUNT() = 0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND'; END IF;
  INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op) VALUES (p_tenant_id, 'TARIFF', p_uuid, 'DELETE');
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'DELETE', 'tariff', p_uuid, NULL, p_ip);
END $$

-- ---------------- Sincronización con BDT (API de integración) ----------------

-- Cambios de los catálogos compartidos del tenant (y de los globales) posteriores al cursor p_since, en orden.
-- Cada fila trae el estado actual del registro (no el del momento del cambio): BDT solo aplica la última versión.
-- Sin actor: lo llama la API de integración con el tenant de la API key (alcance masters.read).
DROP PROCEDURE IF EXISTS sp_catalog_changes $$
CREATE PROCEDURE sp_catalog_changes(IN p_tenant_id BIGINT UNSIGNED, IN p_since BIGINT UNSIGNED, IN p_limit INT)
BEGIN
  SET p_limit = LEAST(GREATEST(IFNULL(p_limit, 500), 1), 1000);
  SELECT x.id AS cursor_id, JSON_OBJECT('cursor', x.id, 'entity', x.entity, 'id', x.entity_uuid, 'op', x.op, 'at', x.created_at,
           'data', CASE x.entity
             WHEN 'MASTER_ITEM' THEN (SELECT JSON_OBJECT('table', t.code, 'code', i.code, 'label', i.label_i18n,
                        'tenantScoped', i.tenant_id IS NOT NULL, 'parentId', par.uuid, 'num1', i.num1, 'num2', i.num2, 'num3', i.num3,
                        'str1', i.str1, 'str2', i.str2, 'str3', i.str3, 'sortOrder', i.sort_order, 'active', i.is_active = 1,
                        'deleted', i.deleted_at IS NOT NULL)
                        FROM master_item i JOIN master_table t ON t.id = i.master_table_id LEFT JOIN master_item par ON par.id = i.parent_item_id
                       WHERE i.uuid = x.entity_uuid)
             WHEN 'CLIENT_CONTACT' THEN (SELECT JSON_OBJECT('clientId', c.uuid, 'firstName', cc.first_name, 'lastName', cc.last_name,
                        'secondLastName', cc.second_last_name, 'position', cc.position, 'phone', cc.phone, 'phone2', cc.phone2,
                        'email', cc.email, 'email2', cc.email2, 'deleted', cc.deleted_at IS NOT NULL)
                        FROM client_contact cc JOIN master_item c ON c.id = cc.client_item_id WHERE cc.uuid = x.entity_uuid)
             WHEN 'TARIFF' THEN (SELECT JSON_OBJECT('clientId', c.uuid, 'profileId', p.uuid, 'currencyId', cu.uuid, 'rateTypeId', rt.uuid,
                        'rate', tf.rate, 'validFrom', tf.valid_from, 'validTo', tf.valid_to, 'deleted', tf.deleted_at IS NOT NULL)
                        FROM tariff tf JOIN master_item p ON p.id = tf.profile_item_id JOIN master_item cu ON cu.id = tf.currency_item_id
                        JOIN master_item rt ON rt.id = tf.rate_type_item_id LEFT JOIN master_item c ON c.id = tf.client_item_id
                       WHERE tf.uuid = x.entity_uuid)
           END) AS doc
    FROM catalog_change x
   WHERE x.tenant_key IN (0, p_tenant_id) AND x.id > IFNULL(p_since, 0)
   ORDER BY x.id
   LIMIT p_limit;
END $$

-- Carga inicial desde BDT (una vez, al activar la integración): clientes y perfiles del tenant y contactos.
-- p_items: [{table, id, code, label, str1, str2, sortOrder, active}], p_contacts: [{id, clientId, firstName, ...}].
-- Un ítem cuyo uuid ya existe se deja igual; si existe otro con el mismo código, no se pisa y se devuelve su uuid para
-- que BDT lo use; si no existe, se crea con el uuid de BDT. Devuelve el mapeo (entidad, id de BDT, id en IGDT).
-- Sin actor: lo llama la API de integración (alcance catalog.import).
DROP PROCEDURE IF EXISTS sp_catalog_import $$
CREATE PROCEDURE sp_catalog_import(IN p_tenant_id BIGINT UNSIGNED, IN p_items JSON, IN p_contacts JSON)
BEGIN
  DECLARE v_done INT DEFAULT 0;
  DECLARE v_table, v_code VARCHAR(60);
  DECLARE v_id, v_existing CHAR(36);
  DECLARE v_label JSON;
  DECLARE v_str1, v_str2 VARCHAR(250);
  DECLARE v_sort INT;
  DECLARE v_active TINYINT(1);
  DECLARE v_table_id BIGINT UNSIGNED;
  DECLARE cur CURSOR FOR
    SELECT j.tbl, j.id, j.code, j.label, j.str1, j.str2, IFNULL(j.sort_order, 0), IFNULL(j.active, 1)
      FROM JSON_TABLE(IFNULL(p_items, JSON_ARRAY()), '$[*]' COLUMNS (tbl VARCHAR(60) PATH '$.table', id CHAR(36) PATH '$.id',
             code VARCHAR(60) PATH '$.code', label JSON PATH '$.label', str1 VARCHAR(250) PATH '$.str1', str2 VARCHAR(250) PATH '$.str2',
             sort_order INT PATH '$.sortOrder', active TINYINT PATH '$.active')) j;
  DECLARE CONTINUE HANDLER FOR NOT FOUND SET v_done = 1;
  IF JSON_LENGTH(IFNULL(p_items, JSON_ARRAY())) > 2000 OR JSON_LENGTH(IFNULL(p_contacts, JSON_ARRAY())) > 5000 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:VALIDATION';
  END IF;
  DROP TEMPORARY TABLE IF EXISTS tmp_import_map;
  CREATE TEMPORARY TABLE tmp_import_map (entity VARCHAR(20), source_id CHAR(36), igdt_id CHAR(36), created TINYINT(1), PRIMARY KEY (entity, source_id)) ENGINE=MEMORY;

  OPEN cur;
  items: LOOP
    FETCH cur INTO v_table, v_id, v_code, v_label, v_str1, v_str2, v_sort, v_active;
    IF v_done = 1 THEN LEAVE items; END IF;
    -- solo los catálogos compartidos que son propios del tenant
    -- SET = (SELECT …) y no SELECT … INTO: sin filas, este último dispararía el NOT FOUND del cursor y cortaría el bucle
    SET v_table_id = (SELECT id FROM master_table WHERE code = v_table AND shared = 1 AND scope IN ('TENANT', 'BOTH') AND deleted_at IS NULL);
    IF v_table_id IS NULL OR v_id IS NULL OR v_code IS NULL OR JSON_TYPE(JSON_EXTRACT(v_label, '$.es')) IS NULL THEN
      ITERATE items;
    END IF;
    SET v_existing = (SELECT uuid FROM master_item WHERE uuid = v_id AND tenant_id = p_tenant_id);
    IF v_existing IS NULL THEN
      SET v_existing = (SELECT uuid FROM master_item
                         WHERE master_table_id = v_table_id AND tenant_key IN (0, p_tenant_id) AND code = v_code AND deleted_at IS NULL
                         ORDER BY tenant_key DESC LIMIT 1);
    END IF;
    IF v_existing IS NULL THEN
      -- se conserva el uuid de BDT salvo que ya lo use otro tenant (p. ej. un perfil global de BDT que usan varios)
      SET v_existing = IF(EXISTS (SELECT 1 FROM master_item WHERE uuid = v_id), UUID(), v_id);
      INSERT INTO master_item (uuid, master_table_id, tenant_id, code, label_i18n, str1, str2, sort_order, is_active)
      VALUES (v_existing, v_table_id, p_tenant_id, v_code, v_label, v_str1, v_str2, v_sort, v_active);
      INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op) VALUES (p_tenant_id, 'MASTER_ITEM', v_existing, 'UPSERT');
      INSERT IGNORE INTO tmp_import_map VALUES ('MASTER_ITEM', v_id, v_existing, 1);
    ELSE
      INSERT IGNORE INTO tmp_import_map VALUES ('MASTER_ITEM', v_id, v_existing, 0);
    END IF;
  END LOOP;
  CLOSE cur;

  -- contactos que ya existían (no se crean ni se marcan como nuevos)
  INSERT IGNORE INTO tmp_import_map
  SELECT 'CLIENT_CONTACT', cc.uuid, cc.uuid, 0
    FROM JSON_TABLE(IFNULL(p_contacts, JSON_ARRAY()), '$[*]' COLUMNS (id CHAR(36) PATH '$.id')) j
    JOIN client_contact cc ON cc.uuid = j.id AND cc.tenant_id = p_tenant_id;
  -- contactos nuevos: el cliente se busca por su id en IGDT (mapeado) o por el de BDT
  INSERT INTO client_contact (uuid, tenant_id, client_item_id, first_name, last_name, second_last_name, position, phone, phone2, email, email2)
  SELECT j.id, p_tenant_id, c.id, LEFT(TRIM(j.first_name), 100), LEFT(TRIM(j.last_name), 100), NULLIF(LEFT(TRIM(j.second_last_name), 100), ''),
         NULLIF(LEFT(TRIM(j.position), 150), ''), NULLIF(LEFT(TRIM(j.phone), 30), ''), NULLIF(LEFT(TRIM(j.phone2), 30), ''),
         NULLIF(LEFT(LOWER(TRIM(j.email)), 254), ''), NULLIF(LEFT(LOWER(TRIM(j.email2)), 254), '')
    FROM JSON_TABLE(IFNULL(p_contacts, JSON_ARRAY()), '$[*]' COLUMNS (id CHAR(36) PATH '$.id', client_id CHAR(36) PATH '$.clientId',
           first_name VARCHAR(200) PATH '$.firstName', last_name VARCHAR(200) PATH '$.lastName', second_last_name VARCHAR(200) PATH '$.secondLastName',
           position VARCHAR(300) PATH '$.position', phone VARCHAR(60) PATH '$.phone', phone2 VARCHAR(60) PATH '$.phone2',
           email VARCHAR(400) PATH '$.email', email2 VARCHAR(400) PATH '$.email2')) j
    JOIN master_item c ON c.uuid = IFNULL((SELECT m.igdt_id FROM tmp_import_map m WHERE m.entity = 'MASTER_ITEM' AND m.source_id = j.client_id), j.client_id)
                      AND c.tenant_id = p_tenant_id AND c.deleted_at IS NULL
    JOIN master_table ct ON ct.id = c.master_table_id AND ct.code = 'CLIENT'
   WHERE j.id IS NOT NULL AND TRIM(IFNULL(j.first_name, '')) <> '' AND TRIM(IFNULL(j.last_name, '')) <> ''
     AND NOT EXISTS (SELECT 1 FROM client_contact x WHERE x.uuid = j.id);
  INSERT IGNORE INTO tmp_import_map
  SELECT 'CLIENT_CONTACT', cc.uuid, cc.uuid, 1
    FROM JSON_TABLE(IFNULL(p_contacts, JSON_ARRAY()), '$[*]' COLUMNS (id CHAR(36) PATH '$.id')) j
    JOIN client_contact cc ON cc.uuid = j.id AND cc.tenant_id = p_tenant_id;
  INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op)
  SELECT p_tenant_id, 'CLIENT_CONTACT', m.igdt_id, 'UPSERT' FROM tmp_import_map m WHERE m.entity = 'CLIENT_CONTACT' AND m.created = 1;
  CALL sp_audit_insert(p_tenant_id, NULL, NULL, 'IMPORT', 'catalog', NULL,
    JSON_OBJECT('items', JSON_LENGTH(IFNULL(p_items, JSON_ARRAY())), 'contacts', JSON_LENGTH(IFNULL(p_contacts, JSON_ARRAY()))), NULL);
  SELECT entity, source_id, igdt_id, created FROM tmp_import_map ORDER BY entity, source_id;
  DROP TEMPORARY TABLE tmp_import_map;
END $$

DELIMITER ;
