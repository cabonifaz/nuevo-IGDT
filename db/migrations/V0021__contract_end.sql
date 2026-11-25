-- Fin de contrato (v0.13): cuando el contrato de un ingreso está por terminar se alerta a sus gestores (los del cliente
-- actual, el gestor dirigido o, si el cliente no tiene gestores, los administradores) para que lo renueven, den de baja,
-- traspasen a otro cliente o lo pasen a otro RQ de BDT.
-- - hire_form_setting.contract_alert_days: días de anticipación de los avisos (JSON, por defecto [30,15,7]).
-- - hire: estado TERMINATED (baja) o RQ_CHANGED (cerrado por cambio a otro RQ), fecha y motivo de cese
--   (TERMINATION_REASON), fecha de devolución del equipo y quién cerró el contrato.
-- - hire_contract_op: cada operación ejecutada sobre el contrato (RENEW | TERMINATE | TRANSFER | RQ_CHANGE) con lo
--   indicado en el asistente (data: entradas, etiquetas y la foto del contrato antes del cambio para reimprimir).
--   status: EXECUTED (hecha) | REQUESTED (cambio de RQ a la espera del nuevo ingreso en BDT) | COMPLETED (ya ingresó
--   en el nuevo RQ) | CANCELLED (reservado).
-- - employee_equipment: equipo del colaborador según la ficha de equipo aprobada: DELIVERED (entregado),
--   PENDING_RETURN (por recuperar tras la baja) o RETURNED (recuperado). Lo usarán las pantallas de Soporte.
-- - hire_form_dispatch: nuevos documentos (RECOVERY: recuperación de equipo, NOTICE: aviso de cambio de RQ) y motivos
--   por operación, enlazados a la operación (contract_op_id) para reenviarlos.
-- - hire_event: RENEWED, TERMINATED, TRANSFERRED y RQ_CHANGE_REQUESTED para BDT (ignora los tipos que no conoce).

ALTER TABLE hire_form_setting ADD COLUMN contract_alert_days JSON NULL AFTER reminder_days;

ALTER TABLE hire DROP CHECK ck_hire_status;
ALTER TABLE hire
  MODIFY COLUMN status VARCHAR(12) NOT NULL DEFAULT 'ACTIVE',
  ADD COLUMN termination_date           DATE            NULL AFTER revoke_reason,
  ADD COLUMN termination_reason_item_id BIGINT UNSIGNED NULL AFTER termination_date,
  ADD COLUMN equipment_return_date      DATE            NULL AFTER termination_reason_item_id,
  ADD COLUMN closed_at                  DATETIME(3)     NULL AFTER equipment_return_date,
  ADD COLUMN closed_by                  BIGINT UNSIGNED NULL AFTER closed_at,
  ADD KEY ix_hire_end (tenant_id, status, end_date),
  ADD CONSTRAINT fk_hire_termination_reason FOREIGN KEY (termination_reason_item_id) REFERENCES master_item(id),
  ADD CONSTRAINT ck_hire_status CHECK (status IN ('ACTIVE', 'REVOKED', 'TERMINATED', 'RQ_CHANGED'));

CREATE TABLE hire_contract_op (
  id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid           CHAR(36)        NOT NULL,
  tenant_id      BIGINT UNSIGNED NOT NULL,
  hire_id        BIGINT UNSIGNED NOT NULL,
  type           VARCHAR(10)     NOT NULL,
  status         VARCHAR(10)     NOT NULL DEFAULT 'EXECUTED',
  effective_date DATE            NOT NULL,
  data           JSON            NOT NULL,
  reason         VARCHAR(500)    NULL,
  created_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by     BIGINT UNSIGNED NULL,
  updated_at     DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_hire_contract_op_uuid (uuid),
  KEY ix_hire_contract_op (hire_id, id),
  KEY ix_hire_contract_op_tenant (tenant_id, type, status),
  CONSTRAINT fk_hire_contract_op_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_contract_op_hire FOREIGN KEY (hire_id) REFERENCES hire(id),
  CONSTRAINT fk_hire_contract_op_user FOREIGN KEY (created_by) REFERENCES app_user(id),
  CONSTRAINT ck_hire_contract_op_type CHECK (type IN ('RENEW', 'TERMINATE', 'TRANSFER', 'RQ_CHANGE')),
  CONSTRAINT ck_hire_contract_op_status CHECK (status IN ('EXECUTED', 'REQUESTED', 'COMPLETED', 'CANCELLED'))
) ENGINE=InnoDB;

CREATE TABLE employee_equipment (
  id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  uuid             CHAR(36)        NOT NULL,
  tenant_id        BIGINT UNSIGNED NOT NULL,
  employee_id      BIGINT UNSIGNED NOT NULL,
  hire_id          BIGINT UNSIGNED NULL,
  type_item_id     BIGINT UNSIGNED NULL,      -- EQUIPMENT_TYPE (NULL: celular u otro ítem sin catálogo)
  label            VARCHAR(200)    NOT NULL,
  details          JSON            NULL,      -- {"processor","ram","disk","brand","accessories"} de la ficha de equipo
  status           VARCHAR(16)     NOT NULL DEFAULT 'DELIVERED',
  delivered_at     DATE            NULL,
  return_due_date  DATE            NULL,
  returned_at      DATE            NULL,
  notes            VARCHAR(500)    NULL,
  created_at       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_by       BIGINT UNSIGNED NULL,
  updated_at       DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by       BIGINT UNSIGNED NULL,
  UNIQUE KEY uq_employee_equipment_uuid (uuid),
  KEY ix_employee_equipment (tenant_id, status),
  KEY ix_employee_equipment_emp (employee_id),
  KEY ix_employee_equipment_hire (hire_id),
  CONSTRAINT fk_employee_equipment_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_employee_equipment_emp FOREIGN KEY (employee_id) REFERENCES employee(id),
  CONSTRAINT fk_employee_equipment_hire FOREIGN KEY (hire_id) REFERENCES hire(id),
  CONSTRAINT fk_employee_equipment_type FOREIGN KEY (type_item_id) REFERENCES master_item(id),
  CONSTRAINT ck_employee_equipment_status CHECK (status IN ('DELIVERED', 'PENDING_RETURN', 'RETURNED'))
) ENGINE=InnoDB;

-- equipo ya entregado en los ingresos vigentes con ficha de equipo aprobada
INSERT INTO employee_equipment (uuid, tenant_id, employee_id, hire_id, type_item_id, label, details, status, delivered_at, created_by)
SELECT UUID(), x.tenant_id, x.employee_id, x.id, i.id, IFNULL(JSON_UNQUOTE(JSON_EXTRACT(i.label_i18n, '$.es')), i.code),
       JSON_OBJECT('processor', JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.processor')),
                   'ram', JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.ram')),
                   'disk', JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.disk')),
                   'brand', JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.brand')),
                   'accessories', JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.accessories'))),
       'DELIVERED',
       IF(JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.deliveryDate')) REGEXP '^[0-9]{4}-[0-9]{2}-[0-9]{2}$',
          CAST(JSON_UNQUOTE(JSON_EXTRACT(x.equipment_request, '$.deliveryDate')) AS DATE), NULL),
       x.created_by
  FROM hire x
  JOIN JSON_TABLE(IFNULL(JSON_EXTRACT(x.equipment_request, '$.types'), JSON_ARRAY()), '$[*]' COLUMNS (u CHAR(36) PATH '$')) j
  JOIN master_item i ON i.uuid = j.u
 WHERE x.status = 'ACTIVE' AND JSON_TYPE(x.equipment_request) = 'OBJECT';

ALTER TABLE hire_form_dispatch DROP CHECK ck_hire_form_dispatch_form, DROP CHECK ck_hire_form_dispatch_reason;
ALTER TABLE hire_form_dispatch
  MODIFY COLUMN reason VARCHAR(12) NOT NULL,
  ADD COLUMN contract_op_id BIGINT UNSIGNED NULL AFTER hire_id,
  ADD CONSTRAINT fk_hire_form_dispatch_op FOREIGN KEY (contract_op_id) REFERENCES hire_contract_op(id),
  ADD CONSTRAINT ck_hire_form_dispatch_form CHECK (form IN ('FMI', 'EQUIPMENT', 'USERS', 'RECOVERY', 'NOTICE')),
  ADD CONSTRAINT ck_hire_form_dispatch_reason CHECK (reason IN ('CONFIRMED', 'UPDATED', 'REVOKED', 'RESENT', 'RENEWED', 'TERMINATED', 'TRANSFERRED', 'RQ_CHANGE'));

ALTER TABLE hire_change DROP CHECK ck_hire_change_type,
  ADD CONSTRAINT ck_hire_change_type CHECK (type IN ('UPDATED', 'START_CHANGED', 'REVOKED', 'RENEWED', 'TERMINATED', 'TRANSFERRED', 'RQ_CHANGED'));

ALTER TABLE hire_event DROP CHECK ck_hire_event_type,
  ADD CONSTRAINT ck_hire_event_type CHECK (type IN ('HIRED', 'START_CHANGED', 'REVOKED', 'RENEWED', 'TERMINATED', 'TRANSFERRED', 'RQ_CHANGE_REQUESTED'));
