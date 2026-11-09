-- Registro del ingreso: el gestor de servicios completa la solicitud de ingreso (contrato, lugar de trabajo,
-- remuneración, SUNAT y equipos) y el talento queda como colaborador de la empresa.
-- - employee: un colaborador por talento de BDT (talent_uuid) y tenant. Datos personales cifrados.
-- - hire: el ingreso (un contrato). La remuneración se guarda cifrada (pay_enc: JSON con los montos) y solo la ven
--   quienes tienen employee.salary.read.
-- - hire_event: lo que BDT debe saber (HIRED; más adelante ceses). BDT lo lee con su token (hires.read) desde un
--   cursor, igual que catalog_change: marca el ingreso del candidato y cierra el RQ cuando se cubren las vacantes.

CREATE TABLE employee (
  id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid              CHAR(36)        NOT NULL,
  tenant_id         BIGINT UNSIGNED NOT NULL,
  talent_uuid       CHAR(36)        NOT NULL,
  first_name        VARCHAR(100)    NOT NULL,
  last_name         VARCHAR(100)    NOT NULL,
  second_last_name  VARCHAR(100)    NULL,
  doc_type_code     VARCHAR(40)     NULL,
  doc_type_label    VARCHAR(100)    NULL,
  doc_number_enc    VARBINARY(512)  NULL,
  doc_number_bidx   CHAR(64)        NULL,
  email_enc         VARBINARY(1024) NULL,
  phone_enc         VARBINARY(512)  NULL,
  status            VARCHAR(12)     NOT NULL DEFAULT 'ACTIVE',
  created_at        DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by        BIGINT UNSIGNED NULL,
  updated_at        DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by        BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_employee_uuid (uuid),
  UNIQUE KEY uq_employee_talent (tenant_id, talent_uuid),
  KEY ix_employee_doc (tenant_id, doc_number_bidx),
  CONSTRAINT fk_employee_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT ck_employee_status CHECK (status IN ('ACTIVE', 'TERMINATED'))
) ENGINE=InnoDB;

CREATE TABLE hire (
  id                        BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid                      CHAR(36)        NOT NULL,
  tenant_id                 BIGINT UNSIGNED NOT NULL,
  hire_request_id           BIGINT UNSIGNED NOT NULL,
  employee_id               BIGINT UNSIGNED NOT NULL,
  client_uuid               CHAR(36)        NULL,
  client_label              VARCHAR(200)    NULL,
  position                  VARCHAR(150)    NOT NULL,
  contract_modality_item_id BIGINT UNSIGNED NOT NULL,
  hire_reason_item_id       BIGINT UNSIGNED NOT NULL,
  area_item_id              BIGINT UNSIGNED NOT NULL,
  work_site_item_id         BIGINT UNSIGNED NULL,
  start_date                DATE            NOT NULL,
  end_date                  DATE            NULL,
  schedule                  VARCHAR(200)    NOT NULL,
  contract_object           VARCHAR(1000)   NULL,
  project_service           VARCHAR(200)    NULL,
  location                  VARCHAR(200)    NULL,
  sunat_declared            TINYINT(1)      NOT NULL DEFAULT 0,
  has_equipment             TINYINT(1)      NOT NULL DEFAULT 0,
  equipment                 JSON            NULL,      -- tipos de equipo a entregar (uuid de EQUIPMENT_TYPE) si no cuenta con equipo
  currency_item_id          BIGINT UNSIGNED NOT NULL,
  pay_enc                   VARBINARY(1024) NOT NULL,  -- {"base","mobility","quarterly","semiannual"} o {"fee"} cifrado
  notes                     VARCHAR(1000)   NULL,
  created_at                DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by                BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_hire_uuid (uuid),
  UNIQUE KEY uq_hire_request (hire_request_id),
  KEY ix_hire_employee (tenant_id, employee_id),
  KEY ix_hire_start (tenant_id, start_date),
  CONSTRAINT fk_hire_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_request FOREIGN KEY (hire_request_id) REFERENCES hire_request(id),
  CONSTRAINT fk_hire_employee FOREIGN KEY (employee_id) REFERENCES employee(id),
  CONSTRAINT fk_hire_contract FOREIGN KEY (contract_modality_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_hire_reason FOREIGN KEY (hire_reason_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_hire_area FOREIGN KEY (area_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_hire_site FOREIGN KEY (work_site_item_id) REFERENCES master_item(id),
  CONSTRAINT fk_hire_currency FOREIGN KEY (currency_item_id) REFERENCES master_item(id),
  CONSTRAINT ck_hire_dates CHECK (end_date IS NULL OR end_date >= start_date)
) ENGINE=InnoDB;

ALTER TABLE hire_request ADD COLUMN hire_id BIGINT UNSIGNED NULL AFTER completed_at,
  ADD CONSTRAINT fk_hire_request_hire FOREIGN KEY (hire_id) REFERENCES hire(id);

CREATE TABLE hire_event (
  id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id      BIGINT UNSIGNED NOT NULL,
  type           VARCHAR(20)     NOT NULL,
  candidate_uuid CHAR(36)        NOT NULL,
  requirement_uuid CHAR(36)      NOT NULL,
  data           JSON            NOT NULL,
  created_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY ix_hire_event (tenant_id, id),
  CONSTRAINT fk_hire_event_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT ck_hire_event_type CHECK (type IN ('HIRED'))
) ENGINE=InnoDB;
