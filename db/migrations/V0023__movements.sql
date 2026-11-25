-- Movimientos (v0.14): el gestor registra en cualquier momento, para un contrato vigente, un movimiento con uno o varios
-- cambios (traspaso de área en el mismo cliente, remuneración, cargo, jornada, sede / lugar / proyecto y fecha de fin).
-- Genera la FMI con la sección MOVIMIENTO. Con fecha efectiva futura queda programado (SCHEDULED) y la rutina diaria lo
-- aplica ese día, igual que la baja programada. El traspaso del asistente de fin de contrato es un movimiento.
-- Evento para BDT: MOVED (el traspaso ya no emite TRANSFERRED).

ALTER TABLE hire_contract_op DROP CHECK ck_hire_contract_op_type;
ALTER TABLE hire_contract_op ADD CONSTRAINT ck_hire_contract_op_type CHECK (type IN ('RENEW', 'TERMINATE', 'TRANSFER', 'RQ_CHANGE', 'MOVEMENT'));

ALTER TABLE hire_form_dispatch DROP CHECK ck_hire_form_dispatch_reason;
ALTER TABLE hire_form_dispatch ADD CONSTRAINT ck_hire_form_dispatch_reason CHECK (reason IN ('CONFIRMED', 'UPDATED', 'REVOKED', 'RESENT',
  'RENEWED', 'TERMINATED', 'TRANSFERRED', 'RQ_CHANGE', 'RESCHEDULED', 'OP_CANCELLED', 'MOVED'));

ALTER TABLE hire_change DROP CHECK ck_hire_change_type;
ALTER TABLE hire_change ADD CONSTRAINT ck_hire_change_type CHECK (type IN ('UPDATED', 'START_CHANGED', 'REVOKED', 'RENEWED', 'TERMINATED',
  'TRANSFERRED', 'RQ_CHANGED', 'TERM_SCHEDULED', 'TERM_RESCHEDULED', 'TERM_CANCELLED', 'MOVED', 'MOVE_SCHEDULED', 'MOVE_RESCHEDULED',
  'MOVE_CANCELLED'));

ALTER TABLE hire_event DROP CHECK ck_hire_event_type;
ALTER TABLE hire_event ADD CONSTRAINT ck_hire_event_type CHECK (type IN ('HIRED', 'START_CHANGED', 'REVOKED', 'RENEWED', 'TERMINATED',
  'TRANSFERRED', 'RQ_CHANGE_REQUESTED', 'MOVED'));
