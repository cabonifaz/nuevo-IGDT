-- Solo el gestor asignado trabaja la solicitud de ingreso (borrador, confirmación, corrección, revocación, reenvío).
-- El administrador de la empresa puede reasignar una solicitud ya tomada por otro gestor.
INSERT INTO permission (code, description) VALUES
 ('hire.assign', 'Reasignar solicitudes de ingreso tomadas por otro gestor');
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code = 'hire.assign'
 WHERE r.code = 'TENANT_ADMIN';
