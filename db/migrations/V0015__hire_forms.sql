-- Fichas de ingreso y enlace de lectura con BDT.
-- - bdt_link: llave de BDT (solo lectura de candidatos: perfil, CV y postulaciones) que BDT entrega automáticamente
--   al conectar la integración. Se guarda cifrada; la URL de BDT es la variable de entorno BDT_API_URL.
-- - Las fichas (FT-GTH-12 ingreso, FT-GS-03 solicitud de equipo y FT-GS-01 creación de usuarios) se editan como
--   borrador en la solicitud (draft; la remuneración aparte y cifrada) hasta que el gestor las confirma: entonces se
--   registra el ingreso y cada ficha se envía a sus destinatarios.
-- - Después de confirmar, el ingreso se puede corregir (o revocar si el cliente lo anula) hasta la fecha de inicio más
--   unos días de gracia (hire_form_setting.edit_grace_days). Cada cambio queda en hire_change y los cambios de fecha de
--   inicio y las revocaciones se avisan a BDT (hire_event START_CHANGED / REVOKED).

CREATE TABLE bdt_link (
  tenant_id    BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  api_key_enc  VARBINARY(1024) NOT NULL,
  api_key_id   BIGINT UNSIGNED NULL,       -- token de IGDT con el que BDT entregó la llave
  linked_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at   DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_bdt_link_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id)
) ENGINE=InnoDB;

ALTER TABLE hire_request
  ADD COLUMN draft         JSON            NULL AFTER doc,
  ADD COLUMN draft_pay_enc VARBINARY(1024) NULL AFTER draft,
  ADD COLUMN draft_at      DATETIME(3)     NULL AFTER draft_pay_enc,
  ADD COLUMN draft_by      BIGINT UNSIGNED NULL AFTER draft_at,
  ADD COLUMN revoked_at    DATETIME(3)     NULL AFTER completed_at,
  DROP CHECK ck_hire_request_status,
  ADD CONSTRAINT ck_hire_request_status CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED', 'REVOKED'));

ALTER TABLE hire
  ADD COLUMN equipment_request JSON          NULL AFTER equipment,  -- FT-GS-03 (null = no se solicita equipo)
  ADD COLUMN user_request      JSON          NULL AFTER equipment_request, -- FT-GS-01 (null = no se crean usuarios)
  ADD COLUMN status            VARCHAR(10)   NOT NULL DEFAULT 'ACTIVE' AFTER notes,
  ADD COLUMN revoked_at        DATETIME(3)   NULL AFTER status,
  ADD COLUMN revoked_by        BIGINT UNSIGNED NULL AFTER revoked_at,
  ADD COLUMN revoke_reason     VARCHAR(500)  NULL AFTER revoked_by,
  ADD COLUMN updated_at        DATETIME(3)   NULL AFTER created_by,
  ADD COLUMN updated_by        BIGINT UNSIGNED NULL AFTER updated_at,
  ADD CONSTRAINT ck_hire_status CHECK (status IN ('ACTIVE', 'REVOKED'));

-- Historial de cambios del ingreso confirmado. changes: [{"field","from","to"}] (la remuneración solo se marca).
CREATE TABLE hire_change (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  hire_id     BIGINT UNSIGNED NOT NULL,
  type        VARCHAR(16)     NOT NULL,
  changes     JSON            NOT NULL,
  reason      VARCHAR(500)    NULL,
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by  BIGINT UNSIGNED NULL,
  KEY ix_hire_change (hire_id, id),
  CONSTRAINT fk_hire_change_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_change_hire FOREIGN KEY (hire_id) REFERENCES hire(id),
  CONSTRAINT ck_hire_change_type CHECK (type IN ('UPDATED', 'START_CHANGED', 'REVOKED'))
) ENGINE=InnoDB;

-- Configuración del tenant: destinatarios de cada ficha, copia al gestor, días de gracia y dominio de correo corporativo.
CREATE TABLE hire_form_setting (
  tenant_id         BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  recipients        JSON            NOT NULL,   -- {"FMI":[…],"EQUIPMENT":[…],"USERS":[…]}
  cc_manager        TINYINT(1)      NOT NULL DEFAULT 1,
  edit_grace_days   INT UNSIGNED    NOT NULL DEFAULT 5,
  corporate_domain  VARCHAR(120)    NULL,
  updated_at        DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by        BIGINT UNSIGNED NULL,
  CONSTRAINT fk_hire_form_setting_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT ck_hire_form_grace CHECK (edit_grace_days <= 60)
) ENGINE=InnoDB;

-- Envíos de fichas: uno por ficha y motivo (confirmación, actualización, revocación o reenvío).
CREATE TABLE hire_form_dispatch (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  hire_id     BIGINT UNSIGNED NOT NULL,
  form        VARCHAR(10)     NOT NULL,
  reason      VARCHAR(10)     NOT NULL,
  recipients  JSON            NOT NULL,
  status      VARCHAR(14)     NOT NULL,
  error       VARCHAR(500)    NULL,
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by  BIGINT UNSIGNED NULL,
  KEY ix_hire_form_dispatch (hire_id, id),
  CONSTRAINT fk_hire_form_dispatch_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_form_dispatch_hire FOREIGN KEY (hire_id) REFERENCES hire(id),
  CONSTRAINT ck_hire_form_dispatch_form CHECK (form IN ('FMI', 'EQUIPMENT', 'USERS')),
  CONSTRAINT ck_hire_form_dispatch_reason CHECK (reason IN ('CONFIRMED', 'UPDATED', 'REVOKED', 'RESENT')),
  CONSTRAINT ck_hire_form_dispatch_status CHECK (status IN ('SENT', 'FAILED', 'NO_MAILER', 'NO_RECIPIENTS'))
) ENGINE=InnoDB;

ALTER TABLE hire_event DROP CHECK ck_hire_event_type,
  ADD CONSTRAINT ck_hire_event_type CHECK (type IN ('HIRED', 'START_CHANGED', 'REVOKED'));

INSERT INTO permission (code, description) VALUES
 ('hire.settings', 'Configurar los destinatarios y plazos de las fichas de ingreso');
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code = 'hire.settings'
 WHERE r.code IN ('TENANT_ADMIN', 'SERVICE_MANAGER');
