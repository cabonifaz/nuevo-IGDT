-- Gestor dirigido: al confirmar el ingreso en BDT, la reclutadora puede dirigir la solicitud a un gestor de IGDT además
-- de los gestores del cliente (p. ej. ciertos RQ de un cliente los atiende otro gestor). Ese gestor la ve en su bandeja,
-- puede aprobar sus fichas y recibe los recordatorios. NULL = solo los gestores del cliente.
ALTER TABLE hire_request ADD COLUMN directed_user_id BIGINT UNSIGNED NULL AFTER assigned_user_id,
  ADD KEY ix_hire_request_directed (tenant_id, directed_user_id),
  ADD CONSTRAINT fk_hire_request_directed FOREIGN KEY (directed_user_id) REFERENCES app_user(id);
