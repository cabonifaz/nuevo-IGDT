-- Maestro único de catálogos: IGDT gobierna los catálogos que comparte con BDT (clientes y contactos, perfiles,
-- tarifario, modalidades de contrato, monedas, modalidad de trabajo, plazos, áreas y motivos). BDT guarda una copia de
-- solo lectura con los mismos uuid y la mantiene al día leyendo catalog_change con su token de integración.
-- - master_table.shared: el catálogo se publica a BDT.
-- - CLIENT (str1 RUC, str2 dirección) y RQ_PROFILE con los mismos códigos de tabla que BDT (la carga inicial importa
--   los de BDT conservando sus uuid).
-- - client_contact: contactos del cliente. tariff: tarifa por perfil, general o de un cliente, con vigencia.
-- - catalog_change: cada alta, cambio o baja de un catálogo compartido (id = cursor de sincronización).

ALTER TABLE master_table ADD COLUMN shared TINYINT(1) NOT NULL DEFAULT 0 AFTER is_system;

INSERT INTO master_table (code, name_i18n, scope, is_system, shared) VALUES
 ('CLIENT',     JSON_OBJECT('es','Clientes','en','Clients'), 'TENANT', 0, 1),
 ('RQ_PROFILE', JSON_OBJECT('es','Perfiles','en','Profiles'), 'BOTH', 0, 1),
 ('RATE_TYPE',  JSON_OBJECT('es','Tipos de tarifa','en','Rate types'), 'GLOBAL', 1, 1);

UPDATE master_table SET shared = 1
 WHERE code IN ('CURRENCY', 'TIME_UNIT', 'WORK_MODALITY', 'CONTRACT_MODALITY', 'AREA', 'HIRE_REASON', 'TERMINATION_REASON');

INSERT INTO master_item (uuid, master_table_id, code, label_i18n, sort_order)
SELECT UUID(), t.id, v.code, v.label, v.ord
FROM master_table t
JOIN (
  SELECT 'RATE_TYPE' tbl, 'HOURLY' code, JSON_OBJECT('es','Por hora','en','Hourly') label, 1 ord UNION ALL
  SELECT 'RATE_TYPE', 'DAILY', JSON_OBJECT('es','Por día','en','Daily'), 2 UNION ALL
  SELECT 'RATE_TYPE', 'MONTHLY', JSON_OBJECT('es','Mensual','en','Monthly'), 3
) v ON v.tbl = t.code;

CREATE TABLE client_contact (
  id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid             CHAR(36)        NOT NULL,
  tenant_id        BIGINT UNSIGNED NOT NULL,
  client_item_id   BIGINT UNSIGNED NOT NULL,
  first_name       VARCHAR(100)    NOT NULL,
  last_name        VARCHAR(100)    NOT NULL,
  second_last_name VARCHAR(100)    NULL,
  position         VARCHAR(150)    NULL,
  phone            VARCHAR(30)     NULL,
  phone2           VARCHAR(30)     NULL,
  email            VARCHAR(254)    NULL,
  email2           VARCHAR(254)    NULL,
  created_at       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by       BIGINT UNSIGNED NULL,
  updated_at       DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by       BIGINT UNSIGNED NULL,
  deleted_at       DATETIME(3)     NULL,
  deleted_by       BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_client_contact_uuid (uuid),
  KEY ix_client_contact (tenant_id, client_item_id),
  CONSTRAINT fk_cc_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_cc_client FOREIGN KEY (client_item_id) REFERENCES master_item(id)
) ENGINE=InnoDB;

-- Tarifa de un perfil: general del tenant (client_item_id NULL) o propia de un cliente; vigente desde valid_from
-- (y hasta valid_to si se indica). Para un cliente y perfil, la propia del cliente prevalece sobre la general.
CREATE TABLE tariff (
  id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid              CHAR(36)        NOT NULL,
  tenant_id         BIGINT UNSIGNED NOT NULL,
  client_item_id    BIGINT UNSIGNED NULL,
  profile_item_id   BIGINT UNSIGNED NOT NULL,
  currency_item_id  BIGINT UNSIGNED NOT NULL,
  rate_type_item_id BIGINT UNSIGNED NOT NULL,
  rate              DECIMAL(12,2)   NOT NULL,
  valid_from        DATE            NOT NULL,
  valid_to          DATE            NULL,
  notes             VARCHAR(300)    NULL,
  created_at        DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by        BIGINT UNSIGNED NULL,
  updated_at        DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by        BIGINT UNSIGNED NULL,
  deleted_at        DATETIME(3)     NULL,
  deleted_by        BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_tariff_uuid (uuid),
  KEY ix_tariff_lookup (tenant_id, profile_item_id, client_item_id, valid_from),
  CONSTRAINT fk_tariff_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_tariff_client FOREIGN KEY (client_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_tariff_profile FOREIGN KEY (profile_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_tariff_currency FOREIGN KEY (currency_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_tariff_rate_type FOREIGN KEY (rate_type_item_id) REFERENCES master_item(id),
  CONSTRAINT ck_tariff_rate CHECK (rate > 0),
  CONSTRAINT ck_tariff_dates CHECK (valid_to IS NULL OR valid_to >= valid_from)
) ENGINE=InnoDB;

-- Cambios de los catálogos compartidos. tenant_id NULL = catálogo global (aplica a todos los tenants).
CREATE TABLE catalog_change (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id   BIGINT UNSIGNED NULL,
  entity      VARCHAR(20)     NOT NULL,
  entity_uuid CHAR(36)        NOT NULL,
  op          VARCHAR(10)     NOT NULL,
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  tenant_key  BIGINT UNSIGNED AS (IFNULL(tenant_id, 0)) STORED,
  KEY ix_catalog_change (tenant_key, id),
  CONSTRAINT ck_catalog_change_entity CHECK (entity IN ('MASTER_ITEM', 'CLIENT_CONTACT', 'TARIFF')),
  CONSTRAINT ck_catalog_change_op CHECK (op IN ('UPSERT', 'DELETE'))
) ENGINE=InnoDB;

-- los catálogos compartidos ya cargados entran al historial (BDT los recibe en su primera sincronización)
INSERT INTO catalog_change (tenant_id, entity, entity_uuid, op)
SELECT i.tenant_id, 'MASTER_ITEM', i.uuid, 'UPSERT'
  FROM master_item i JOIN master_table t ON t.id = i.master_table_id AND t.shared = 1
 WHERE i.deleted_at IS NULL ORDER BY i.id;

INSERT INTO permission (code, description) VALUES
 ('catalog.manage', 'Administrar los catálogos compartidos con BDT: clientes y contactos, perfiles, tarifario y motivos');
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code = 'catalog.manage' WHERE r.code IN ('TENANT_ADMIN', 'SERVICE_MANAGER');

INSERT INTO feature (code, name_i18n, description_i18n, module, impacted_roles, is_core, default_enabled, sort_order, sa_notified_at) VALUES
 ('catalogs', JSON_OBJECT('es','Catálogos maestros','en','Master catalogs'),
   JSON_OBJECT('es','Clientes y contactos, perfiles, tarifario y motivos: IGDT los gobierna y BDT los usa en solo lectura.',
               'en','Clients and contacts, profiles, rates and reasons: IGDT governs them and BDT uses them read-only.'),
   'catalogs', JSON_ARRAY('TENANT_ADMIN','SERVICE_MANAGER'), 1, 1, 5, NOW(3));
