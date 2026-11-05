-- =====================================================================
-- Datos base de IGDT: roles, permisos, maestros de plataforma y tenant Fractal
-- =====================================================================

INSERT INTO role (code, name_i18n, is_platform) VALUES
 ('SUPER_ADMIN',     JSON_OBJECT('es','Super administrador','en','Super administrator'), 1),
 ('TENANT_ADMIN',    JSON_OBJECT('es','Administrador','en','Administrator'), 0),
 ('SERVICE_MANAGER', JSON_OBJECT('es','Gestor de servicios','en','Service manager'), 0);

INSERT INTO permission (code, description) VALUES
 ('platform.tenants.manage',  'Crear, editar y suspender tenants'),
 ('platform.apikeys.approve', 'Habilitar o revocar API keys de cualquier tenant'),
 ('platform.audit.read',      'Ver auditoría de toda la plataforma'),
 ('platform.masters.manage',  'Administrar maestros globales'),
 ('tenant.users.manage',      'Administrar usuarios del tenant'),
 ('tenant.users.invite',      'Invitar usuarios al tenant'),
 ('tenant.branding.manage',   'Configurar logos, colores y plantilla de documentos del tenant'),
 ('tenant.masters.manage',    'Administrar maestros propios del tenant'),
 ('tenant.reports.manage',    'Configurar formatos y reportes del tenant'),
 ('tenant.translations.manage','Personalizar textos del tenant'),
 ('tenant.apikeys.request',   'Solicitar API keys de integración'),
 ('tenant.audit.read',        'Ver auditoría del tenant'),
 ('hire.read',                'Ver la bandeja de ingresos'),
 ('hire.write',               'Completar y registrar ingresos'),
 ('employee.read',            'Ver colaboradores y su historial'),
 ('employee.write',           'Registrar movimientos y ceses'),
 ('employee.salary.read',     'Ver remuneraciones y estructura salarial'),
 ('equipment.manage',         'Registrar y atender solicitudes de equipo');

-- Super Admin: todo lo de plataforma
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON p.code LIKE 'platform.%' WHERE r.code = 'SUPER_ADMIN';
-- Admin del tenant: administración de la empresa y todo el negocio
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON (p.code LIKE 'tenant.%' OR p.code LIKE 'hire.%' OR p.code LIKE 'employee.%' OR p.code LIKE 'equipment.%')
 WHERE r.code = 'TENANT_ADMIN';
-- Gestor de servicios: ingresos, colaboradores (con remuneraciones) y equipos
INSERT INTO role_permission (role_id, permission_id)
SELECT r.id, p.id FROM role r JOIN permission p ON (p.code LIKE 'hire.%' OR p.code LIKE 'employee.%' OR p.code LIKE 'equipment.%')
 WHERE r.code = 'SERVICE_MANAGER';

-- ---------------------------------------------------------------------
-- Maestro de maestros: catálogos de plataforma conocidos.
-- El resto de catálogos (y sus legacy_id) se importa desde el sistema
-- anterior con la herramienta de migración.
-- ---------------------------------------------------------------------
INSERT INTO master_table (code, name_i18n, scope, is_system) VALUES
 ('CURRENCY',           JSON_OBJECT('es','Monedas','en','Currencies'), 'GLOBAL', 1),
 ('COUNTRY',            JSON_OBJECT('es','Países','en','Countries'), 'GLOBAL', 1),
 ('IDENTITY_DOC_TYPE',  JSON_OBJECT('es','Tipos de documento de identidad','en','Identity document types'), 'GLOBAL', 1),
 ('TIME_UNIT',          JSON_OBJECT('es','Unidades de tiempo','en','Time units'), 'GLOBAL', 1),
 ('WORK_MODALITY',      JSON_OBJECT('es','Modalidades de trabajo','en','Work modalities'), 'GLOBAL', 1),
 ('CONTRACT_MODALITY',  JSON_OBJECT('es','Modalidades de contrato','en','Contract modalities'), 'GLOBAL', 1),
 ('AREA',               JSON_OBJECT('es','Áreas o unidades','en','Areas'), 'BOTH', 0),
 ('HIRE_REASON',        JSON_OBJECT('es','Motivos de ingreso','en','Hire reasons'), 'BOTH', 0),
 ('TERMINATION_REASON', JSON_OBJECT('es','Motivos de cese','en','Termination reasons'), 'BOTH', 0),
 ('EQUIPMENT_TYPE',     JSON_OBJECT('es','Tipos de equipo','en','Equipment types'), 'BOTH', 0),
 ('EQUIPMENT_ANNEX',    JSON_OBJECT('es','Anexos de equipo','en','Equipment annexes'), 'BOTH', 0),
 ('SOFTWARE',           JSON_OBJECT('es','Software','en','Software'), 'BOTH', 0),
 ('WORK_SITE',          JSON_OBJECT('es','Sedes para declarar','en','Work sites'), 'BOTH', 0);

INSERT INTO master_table (code, name_i18n, scope, is_system, parent_table_id)
SELECT 'CITY', JSON_OBJECT('es','Ciudades','en','Cities'), 'GLOBAL', 1, id FROM master_table WHERE code = 'COUNTRY';

-- str1 de CONTRACT_MODALITY: grupo PAYROLL (planilla) o RXH (locación de servicios, recibo por honorarios)
INSERT INTO master_item (uuid, master_table_id, code, label_i18n, str1, sort_order)
SELECT UUID(), t.id, v.code, v.label, v.s1, v.ord
FROM master_table t
JOIN (
  SELECT 'CURRENCY' tbl, 'PEN' code, JSON_OBJECT('es','Soles','en','Peruvian sol') label, NULL s1, 1 ord UNION ALL
  SELECT 'CURRENCY', 'USD', JSON_OBJECT('es','Dólares','en','US dollar'), NULL, 2 UNION ALL
  SELECT 'COUNTRY', 'PE', JSON_OBJECT('es','Perú','en','Peru'), NULL, 1 UNION ALL
  SELECT 'IDENTITY_DOC_TYPE', 'DNI', JSON_OBJECT('es','DNI','en','National ID'), NULL, 1 UNION ALL
  SELECT 'IDENTITY_DOC_TYPE', 'CE', JSON_OBJECT('es','Carné de extranjería','en','Foreigner card'), NULL, 2 UNION ALL
  SELECT 'IDENTITY_DOC_TYPE', 'CPP', JSON_OBJECT('es','Permiso temporal de permanencia','en','Temporary residence permit'), NULL, 3 UNION ALL
  SELECT 'IDENTITY_DOC_TYPE', 'PASSPORT', JSON_OBJECT('es','Pasaporte','en','Passport'), NULL, 4 UNION ALL
  SELECT 'TIME_UNIT', 'DAYS', JSON_OBJECT('es','Días','en','Days'), NULL, 1 UNION ALL
  SELECT 'TIME_UNIT', 'WEEKS', JSON_OBJECT('es','Semanas','en','Weeks'), NULL, 2 UNION ALL
  SELECT 'TIME_UNIT', 'MONTHS', JSON_OBJECT('es','Meses','en','Months'), NULL, 3 UNION ALL
  SELECT 'WORK_MODALITY', 'ON_SITE', JSON_OBJECT('es','Presencial','en','On site'), NULL, 1 UNION ALL
  SELECT 'WORK_MODALITY', 'REMOTE', JSON_OBJECT('es','Remoto','en','Remote'), NULL, 2 UNION ALL
  SELECT 'WORK_MODALITY', 'HYBRID', JSON_OBJECT('es','Híbrido','en','Hybrid'), NULL, 3 UNION ALL
  SELECT 'CONTRACT_MODALITY', 'RXH', JSON_OBJECT('es','Locación de servicios','en','Service contract'), 'RXH', 1 UNION ALL
  SELECT 'CONTRACT_MODALITY', 'PAYROLL_FULL', JSON_OBJECT('es','Planilla régimen general','en','Payroll (general regime)'), 'PAYROLL', 2 UNION ALL
  SELECT 'CONTRACT_MODALITY', 'PAYROLL_PARTIAL', JSON_OBJECT('es','Planilla tiempo parcial','en','Payroll (part time)'), 'PAYROLL', 3 UNION ALL
  SELECT 'CONTRACT_MODALITY', 'PRE_INTERNSHIP', JSON_OBJECT('es','Prácticas preprofesionales','en','Pre-professional internship'), 'PAYROLL', 4 UNION ALL
  SELECT 'CONTRACT_MODALITY', 'INTERNSHIP', JSON_OBJECT('es','Prácticas profesionales','en','Professional internship'), 'PAYROLL', 5
) v ON v.tbl = t.code;

-- ---------------------------------------------------------------------
-- Tenant Fractal (primera versión: mismo logo y colores que producción)
-- ---------------------------------------------------------------------
INSERT INTO tenant (uuid, slug, name, legal_name, default_locale, timezone)
VALUES (UUID(), 'fractal', 'Fractal', 'Fractal Soluciones TI', 'es', 'America/Lima');

INSERT INTO tenant_branding (tenant_id, display_name, logo_url, color_primary, color_primary_hover,
                             color_secondary, color_accent, color_sidebar, font_family)
SELECT id, 'Fractal - Ingreso y gestión de talentos', '/tenants/fractal/logo.png',
       '#009688', '#2D8D8D', '#0B85C3', '#FAAB34', '#FFFFFF', 'Inter'
FROM tenant WHERE slug = 'fractal';
