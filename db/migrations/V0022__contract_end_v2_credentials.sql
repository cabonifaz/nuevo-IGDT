-- Fin de contrato v2 y credenciales (v0.14):
-- - hire_contract_op: estado SCHEDULED (baja con fecha de cese futura: el contrato sigue vigente hasta esa fecha y pasa a
--   TERMINATED solo, con la rutina diaria) y updated_by (quien cambió la fecha; firma los documentos reenviados).
-- - hire_form_dispatch: motivos RESCHEDULED (cambio de fecha de una baja programada) y OP_CANCELLED (baja anulada).
-- - hire_change: TERM_SCHEDULED, TERM_RESCHEDULED y TERM_CANCELLED.
-- - employee_credential: usuario de red y correo de cada colaborador (ACTIVE, DEACTIVATION_REQUESTED o DEACTIVATED), con
--   su origen (USERS_FORM: ficha de usuarios aprobada, MIGRATION) y fechas. Un ingreso solo exige la ficha de usuarios si
--   el talento no tiene credenciales activas. Base de las futuras pantallas de Soporte.

ALTER TABLE hire_contract_op DROP CHECK ck_hire_contract_op_status;
ALTER TABLE hire_contract_op
  ADD COLUMN updated_by BIGINT UNSIGNED NULL AFTER updated_at,
  ADD CONSTRAINT ck_hire_contract_op_status CHECK (status IN ('EXECUTED', 'SCHEDULED', 'REQUESTED', 'COMPLETED', 'CANCELLED'));

ALTER TABLE hire_form_dispatch DROP CHECK ck_hire_form_dispatch_reason;
ALTER TABLE hire_form_dispatch ADD CONSTRAINT ck_hire_form_dispatch_reason CHECK (reason IN ('CONFIRMED', 'UPDATED', 'REVOKED', 'RESENT',
  'RENEWED', 'TERMINATED', 'TRANSFERRED', 'RQ_CHANGE', 'RESCHEDULED', 'OP_CANCELLED'));

ALTER TABLE hire_change DROP CHECK ck_hire_change_type;
ALTER TABLE hire_change ADD CONSTRAINT ck_hire_change_type CHECK (type IN ('UPDATED', 'START_CHANGED', 'REVOKED', 'RENEWED', 'TERMINATED',
  'TRANSFERRED', 'RQ_CHANGED', 'TERM_SCHEDULED', 'TERM_RESCHEDULED', 'TERM_CANCELLED'));

CREATE TABLE employee_credential (
  id                         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid                       CHAR(36)        NOT NULL,
  tenant_id                  BIGINT UNSIGNED NOT NULL,
  employee_id                BIGINT UNSIGNED NOT NULL,
  hire_request_id            BIGINT UNSIGNED NULL,      -- solicitud cuya ficha de usuarios la creó
  username                   VARCHAR(60)     NULL,
  email                      VARCHAR(150)    NULL,
  status                     VARCHAR(24)     NOT NULL DEFAULT 'ACTIVE',
  source                     VARCHAR(16)     NOT NULL,
  activated_at               DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  deactivation_requested_at  DATETIME(3)     NULL,
  deactivated_at             DATETIME(3)     NULL,
  created_at                 DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by                 BIGINT UNSIGNED NULL,
  updated_at                 DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by                 BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_employee_credential_uuid (uuid),
  KEY ix_employee_credential_emp (employee_id, status),
  KEY ix_employee_credential_tenant (tenant_id, status),
  KEY ix_employee_credential_request (hire_request_id),
  CONSTRAINT fk_employee_credential_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_employee_credential_emp FOREIGN KEY (employee_id) REFERENCES employee(id),
  CONSTRAINT fk_employee_credential_request FOREIGN KEY (hire_request_id) REFERENCES hire_request(id),
  CONSTRAINT ck_employee_credential_status CHECK (status IN ('ACTIVE', 'DEACTIVATION_REQUESTED', 'DEACTIVATED')),
  CONSTRAINT ck_employee_credential_source CHECK (source IN ('USERS_FORM', 'MIGRATION'))
) ENGINE=InnoDB;

-- credenciales de los ingresos con ficha de usuarios aprobada (el último ingreso de cada colaborador)
INSERT INTO employee_credential (uuid, tenant_id, employee_id, hire_request_id, username, email, status, source, activated_at,
                                 deactivation_requested_at, created_by)
SELECT UUID(), x.tenant_id, x.employee_id, x.hire_request_id,
       NULLIF(JSON_UNQUOTE(JSON_EXTRACT(x.user_request, '$.username')), ''), NULLIF(JSON_UNQUOTE(JSON_EXTRACT(x.user_request, '$.email')), ''),
       IF(x.status = 'TERMINATED', 'DEACTIVATION_REQUESTED', 'ACTIVE'), 'MIGRATION', x.created_at,
       IF(x.status = 'TERMINATED', IFNULL(x.closed_at, NOW(3)), NULL), x.created_by
  FROM hire x
 WHERE JSON_TYPE(x.user_request) = 'OBJECT' AND x.status IN ('ACTIVE', 'TERMINATED', 'RQ_CHANGED')
   AND x.id = (SELECT MAX(x2.id) FROM hire x2 WHERE x2.employee_id = x.employee_id AND JSON_TYPE(x2.user_request) = 'OBJECT');
