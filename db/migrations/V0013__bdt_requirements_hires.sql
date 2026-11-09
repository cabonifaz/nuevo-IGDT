-- Bandeja de selección: BDT publica en IGDT (por la API de integración, con el token del tenant) una copia de solo
-- lectura de sus requerimientos con el avance de los postulantes, y una solicitud de ingreso cada vez que Selección
-- confirma a un candidato. IGDT nunca escribe en BDT ni lee su base de datos.
-- - bdt_requirement: último estado del RQ recibido (doc = snapshot completo: vacantes, tarifas y postulantes por etapa).
--   source_updated_at evita que un envío viejo pise uno más nuevo.
-- - hire_request: solicitud de ingreso por candidato confirmado. Los datos personales llegan en claro por TLS y se
--   guardan cifrados con la llave de IGDT (documento, correo y teléfono); el número de documento además con blind index.
--   PENDING (recibida) -> IN_PROGRESS (un gestor la tomó) -> COMPLETED (ingreso registrado, siguiente fase).
--   CANCELLED: Selección retiró la confirmación en BDT; si lo vuelve a confirmar, la solicitud se reabre.

CREATE TABLE bdt_requirement (
  id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id         BIGINT UNSIGNED NOT NULL,
  uuid              CHAR(36)        NOT NULL,
  code              VARCHAR(40)     NOT NULL,
  title             VARCHAR(200)    NOT NULL,
  client_uuid       CHAR(36)        NULL,
  client_label      VARCHAR(200)    NULL,
  status_code       VARCHAR(40)     NOT NULL,
  status_label      VARCHAR(100)    NOT NULL,
  status_group      VARCHAR(10)     NOT NULL,
  request_date      DATE            NULL,
  due_date          DATE            NULL,
  vacancies         INT UNSIGNED    NOT NULL DEFAULT 0,
  covered           INT UNSIGNED    NOT NULL DEFAULT 0,
  candidates        INT UNSIGNED    NOT NULL DEFAULT 0,
  doc               JSON            NOT NULL,
  source_updated_at DATETIME(3)     NOT NULL,
  received_at       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at        DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at        DATETIME(3)     NULL,
  UNIQUE KEY uq_bdt_rq (tenant_id, uuid),
  KEY ix_bdt_rq_inbox (tenant_id, deleted_at, status_group, due_date),
  CONSTRAINT fk_bdt_rq_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT ck_bdt_rq_group CHECK (status_group IN ('OPEN', 'CLOSED'))
) ENGINE=InnoDB;

CREATE TABLE hire_request (
  id                 BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid               CHAR(36)        NOT NULL,
  tenant_id          BIGINT UNSIGNED NOT NULL,
  candidate_uuid     CHAR(36)        NOT NULL,
  requirement_uuid   CHAR(36)        NOT NULL,
  talent_uuid        CHAR(36)        NOT NULL,
  status             VARCHAR(12)     NOT NULL DEFAULT 'PENDING',
  first_name         VARCHAR(100)    NOT NULL,
  last_name          VARCHAR(100)    NOT NULL,
  second_last_name   VARCHAR(100)    NULL,
  headline           VARCHAR(200)    NULL,
  doc_type_code      VARCHAR(40)     NULL,
  doc_type_label     VARCHAR(100)    NULL,
  doc_number_enc     VARBINARY(512)  NULL,
  doc_number_bidx    CHAR(64)        NULL,
  email_enc          VARBINARY(1024) NULL,
  phone_enc          VARBINARY(512)  NULL,
  client_uuid        CHAR(36)        NULL,
  client_label       VARCHAR(200)    NULL,
  profile_uuid       CHAR(36)        NULL,
  profile_label      VARCHAR(200)    NULL,
  doc                JSON            NOT NULL,
  confirmed_at       DATETIME(3)     NOT NULL,
  received_at        DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  assigned_user_id   BIGINT UNSIGNED NULL,
  taken_at           DATETIME(3)     NULL,
  cancelled_at       DATETIME(3)     NULL,
  completed_at       DATETIME(3)     NULL,
  updated_at         DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_hire_request_uuid (uuid),
  UNIQUE KEY uq_hire_request_candidate (tenant_id, candidate_uuid),
  KEY ix_hire_request_inbox (tenant_id, status, received_at),
  KEY ix_hire_request_rq (tenant_id, requirement_uuid),
  KEY ix_hire_request_doc (tenant_id, doc_number_bidx),
  CONSTRAINT fk_hire_request_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_request_user FOREIGN KEY (assigned_user_id) REFERENCES app_user(id),
  CONSTRAINT ck_hire_request_status CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED'))
) ENGINE=InnoDB;

INSERT INTO feature (code, name_i18n, description_i18n, module, impacted_roles, is_core, default_enabled, sort_order, sa_notified_at) VALUES
 ('selection_inbox', JSON_OBJECT('es','Bandeja de selección','en','Selection inbox'),
   JSON_OBJECT('es','Requerimientos de BDT con el avance de sus postulantes y las solicitudes de ingreso de los candidatos confirmados por Selección.',
               'en','BDT requirements with their candidates'' progress and the hire requests for candidates confirmed by Recruiting.'),
   'hires', JSON_ARRAY('TENANT_ADMIN','SERVICE_MANAGER'), 0, 1, 6, NULL);
