-- Aprobación de ingresos por ficha y por los gestores del cliente:
-- - client_manager: cada cliente (ítem CLIENT, mismo uuid que en BDT) tiene un gestor principal y uno secundario.
--   Cualquiera de los dos (o un administrador) aprueba las fichas de las solicitudes de ese cliente. Un cliente sin
--   gestores lo atienden todos los gestores del tenant.
-- - hire_form_approval: aprobación de cada ficha (FMI, USERS, EQUIPMENT) con lo aprobado (data: las fichas sin la
--   remuneración) y quién la aprobó. Al aprobar se envía esa ficha por correo. La solicitud de equipo exige la de
--   usuarios aprobada. La solicitud queda COMPLETED cuando están aprobadas FMI, USERS y, si aplica, EQUIPMENT.
--   data NULL = aprobación registrada antes de este cambio (se imprime con el ingreso).
-- - hire_form_setting: destinatarios por grupo (soporte, talento y copias fijas) y cada cuántos días se recuerda por
--   correo a los gestores las solicitudes pendientes.
-- - hire_reminder_run: última corrida de la notificación diaria y del correo de pendientes por tenant.

CREATE TABLE client_manager (
  tenant_id   BIGINT UNSIGNED NOT NULL,
  client_uuid CHAR(36)        NOT NULL,
  role        VARCHAR(10)     NOT NULL,
  user_id     BIGINT UNSIGNED NOT NULL,
  updated_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by  BIGINT UNSIGNED NULL,
  PRIMARY KEY (tenant_id, client_uuid, role),
  KEY ix_client_manager_user (tenant_id, user_id),
  CONSTRAINT fk_client_manager_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_client_manager_user FOREIGN KEY (user_id) REFERENCES app_user(id),
  CONSTRAINT ck_client_manager_role CHECK (role IN ('PRIMARY', 'SECONDARY'))
) ENGINE=InnoDB;

CREATE TABLE hire_form_approval (
  id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id       BIGINT UNSIGNED NOT NULL,
  hire_request_id BIGINT UNSIGNED NOT NULL,
  form            VARCHAR(10)     NOT NULL,
  data            JSON            NULL,
  approved_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  approved_by     BIGINT UNSIGNED NULL,
  updated_at      DATETIME(3)     NULL,
  updated_by      BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_hire_form_approval (hire_request_id, form),
  KEY ix_hire_form_approval_tenant (tenant_id),
  CONSTRAINT fk_hire_form_approval_request FOREIGN KEY (hire_request_id) REFERENCES hire_request(id),
  CONSTRAINT fk_hire_form_approval_user FOREIGN KEY (approved_by) REFERENCES app_user(id),
  CONSTRAINT ck_hire_form_approval_form CHECK (form IN ('FMI', 'USERS', 'EQUIPMENT'))
) ENGINE=InnoDB;

-- ingresos ya confirmados: sus fichas quedan aprobadas por quien confirmó
INSERT INTO hire_form_approval (tenant_id, hire_request_id, form, data, approved_at, approved_by)
SELECT h.tenant_id, h.hire_request_id, 'FMI', NULL, h.created_at, h.created_by FROM hire h;
INSERT INTO hire_form_approval (tenant_id, hire_request_id, form, data, approved_at, approved_by)
SELECT h.tenant_id, h.hire_request_id, 'USERS', NULL, h.created_at, h.created_by FROM hire h WHERE h.user_request IS NOT NULL;
INSERT INTO hire_form_approval (tenant_id, hire_request_id, form, data, approved_at, approved_by)
SELECT h.tenant_id, h.hire_request_id, 'EQUIPMENT', NULL, h.created_at, h.created_by FROM hire h WHERE h.equipment_request IS NOT NULL;

-- ¿la solicitud lleva solicitud de equipo? NULL = aún no se decide (se toma del borrador o de la condición laptop de BDT)
ALTER TABLE hire_request ADD COLUMN equipment_required TINYINT(1) NULL AFTER draft_by;

ALTER TABLE hire_form_setting
  ADD COLUMN support_emails JSON NULL AFTER recipients,
  ADD COLUMN talent_emails  JSON NULL AFTER support_emails,
  ADD COLUMN cc_emails      JSON NULL AFTER talent_emails,
  ADD COLUMN reminder_days  TINYINT UNSIGNED NOT NULL DEFAULT 2 AFTER edit_grace_days;

-- los destinatarios por ficha pasan a grupos: soporte (usuarios y equipo) y talento (FMI)
UPDATE hire_form_setting
   SET support_emails = (SELECT JSON_ARRAYAGG(e) FROM (
                           SELECT DISTINCT j.e FROM JSON_TABLE(JSON_MERGE_PRESERVE(IFNULL(JSON_EXTRACT(recipients, '$.USERS'), JSON_ARRAY()),
                                                                                    IFNULL(JSON_EXTRACT(recipients, '$.EQUIPMENT'), JSON_ARRAY())),
                                                               '$[*]' COLUMNS (e VARCHAR(150) PATH '$')) j) x),
       talent_emails = IFNULL(JSON_EXTRACT(recipients, '$.FMI'), JSON_ARRAY());

CREATE TABLE hire_reminder_run (
  tenant_id BIGINT UNSIGNED NOT NULL,
  kind      VARCHAR(10)     NOT NULL,
  last_run  DATE            NOT NULL,
  ran_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, kind),
  CONSTRAINT fk_hire_reminder_run_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT ck_hire_reminder_run_kind CHECK (kind IN ('DAILY', 'EMAIL'))
) ENGINE=InnoDB;

-- gestores por cliente: los asigna el administrador de la empresa
INSERT INTO permission (code, description) VALUES ('client.managers.manage', 'Asignar los gestores de cada cliente');
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code = 'client.managers.manage' WHERE r.code = 'TENANT_ADMIN';

-- envíos por solicitud: la ficha de usuarios puede salir antes de registrar el ingreso (FMI)
ALTER TABLE hire_form_dispatch ADD COLUMN hire_request_id BIGINT UNSIGNED NULL AFTER tenant_id,
  MODIFY COLUMN hire_id BIGINT UNSIGNED NULL;
UPDATE hire_form_dispatch d JOIN hire h ON h.id = d.hire_id SET d.hire_request_id = h.hire_request_id;
ALTER TABLE hire_form_dispatch ADD KEY ix_hire_form_dispatch_request (hire_request_id),
  ADD CONSTRAINT fk_hire_form_dispatch_request FOREIGN KEY (hire_request_id) REFERENCES hire_request(id);
