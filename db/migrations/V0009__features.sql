-- =====================================================================
-- Catálogo de funcionalidades
-- - feature: cada funcionalidad de la plataforma (módulo, roles a los que impacta). status SUSPENDED la apaga para
--   todos los tenants; is_core = no se puede suspender. default_enabled: si los tenants la tienen sin activarla.
--   Las funcionalidades nuevas llegan con una migración (default_enabled 0, sa_notified_at NULL): al iniciar, la API
--   avisa al Super Admin; luego él la activa por tenant.
-- - tenant_feature: activación o suspensión por tenant (sin fila = default_enabled).
-- Al activarla en un tenant se notifica a sus usuarios con los roles impactados.
-- =====================================================================

CREATE TABLE feature (
  id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  code             VARCHAR(40)     NOT NULL,
  name_i18n        JSON            NOT NULL,
  description_i18n JSON            NOT NULL,
  module           VARCHAR(40)     NOT NULL,
  impacted_roles   JSON            NOT NULL,
  is_core          TINYINT(1)      NOT NULL DEFAULT 0,
  default_enabled  TINYINT(1)      NOT NULL DEFAULT 1,
  status           VARCHAR(10)     NOT NULL DEFAULT 'ACTIVE',
  sort_order       INT             NOT NULL DEFAULT 0,
  released_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  sa_notified_at   DATETIME(3)     NULL,
  updated_at       DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by       BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_feature_code (code),
  CONSTRAINT ck_feature_status CHECK (status IN ('ACTIVE','SUSPENDED'))
) ENGINE=InnoDB;

CREATE TABLE tenant_feature (
  tenant_id  BIGINT UNSIGNED NOT NULL,
  feature_id BIGINT UNSIGNED NOT NULL,
  enabled    TINYINT(1)      NOT NULL,
  changed_by BIGINT UNSIGNED NULL,
  changed_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, feature_id),
  CONSTRAINT fk_tf_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_tf_feature FOREIGN KEY (feature_id) REFERENCES feature(id)
) ENGINE=InnoDB;

INSERT INTO permission (code, description) VALUES ('platform.features.manage', 'Activar y suspender funcionalidades de la plataforma y por tenant');
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code = 'platform.features.manage' WHERE r.code = 'SUPER_ADMIN';

-- Catálogo inicial (ya conocido por el Super Admin). Los módulos de negocio se agregan con su migración.
INSERT INTO feature (code, name_i18n, description_i18n, module, impacted_roles, is_core, default_enabled, sort_order, sa_notified_at) VALUES
 ('push', JSON_OBJECT('es','Notificaciones push','en','Push notifications'),
   JSON_OBJECT('es','Avisos del sistema operativo en la app instalada.','en','Operating system notifications in the installed app.'),
   'notifications', JSON_ARRAY('TENANT_ADMIN','SERVICE_MANAGER'), 0, 1, 1, NOW(3)),
 ('support', JSON_OBJECT('es','Soporte y sugerencias','en','Support and suggestions'),
   JSON_OBJECT('es','Reportar incidencias, observaciones y mejoras al equipo de la plataforma desde cualquier pantalla.','en','Report incidents, observations and improvements to the platform team from any screen.'),
   'support', JSON_ARRAY('TENANT_ADMIN','SERVICE_MANAGER'), 0, 1, 2, NOW(3)),
 ('api_keys', JSON_OBJECT('es','API de integración','en','Integration API'),
   JSON_OBJECT('es','Tokens de integración con BDT y otros sistemas (aprobados por el Super Admin).','en','Integration tokens for BDT and other systems (approved by the Super Admin).'),
   'administration', JSON_ARRAY('TENANT_ADMIN'), 0, 1, 3, NOW(3)),
 ('administration', JSON_OBJECT('es','Administración de la empresa','en','Company administration'),
   JSON_OBJECT('es','Usuarios, marca y maestros de la empresa.','en','Users, brand and company lists.'),
   'administration', JSON_ARRAY('TENANT_ADMIN'), 1, 1, 4, NOW(3));
