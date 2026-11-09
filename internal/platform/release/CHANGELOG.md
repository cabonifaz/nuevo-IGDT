# Registro de versiones de IGDT (Ingreso y Gestión de Talentos)

Formato: `## vMAYOR.MENOR — AAAA-MM-DD`, seguido de los cambios en viñetas (español). Al publicar una versión se
sube `VERSION` y se agrega su sección aquí: la API la registra al desplegarse en cada ambiente y la notifica.

## v0.2 — 2026-11-09
- Bandeja de selección: los requerimientos que Selección gestiona en BDT llegan solos a IGDT con sus vacantes, tarifas y el tablero de postulantes por etapa (solo lectura), con indicadores de vencidos, cobertura y solicitudes por atender.
- Solicitudes de ingreso: cada candidato que Selección confirma en BDT crea una solicitud con los datos del talento (cifrados), el requerimiento, el cliente, el perfil y la tarifa; el gestor de servicios la toma y, si Selección retira la confirmación, se cancela sola.
- Avisos en la app y push para los gestores (agrupados cuando llegan muchos a la vez) y nuevo alcance de token `requirements.write`.

## v0.1 — 2026-11-05
- Base de la plataforma: empresas (tenants), usuarios por invitación con roles Super Admin, Administrador y Gestor de servicios.
- Marca por empresa (logos, colores y plantilla de documentos), maestro de maestros y textos por empresa.
- Notificaciones en la app y push, soporte al Super Admin, novedades, guías de primer uso y control de versiones.
- Catálogo de funcionalidades por empresa y tokens de integración (API keys) para conectar con BDT.
- Maestro único de catálogos: IGDT gobierna clientes y contactos, perfiles, tarifario (por perfil, general o por cliente, con vigencia), modalidades de contrato, monedas, modalidad de trabajo, plazos, áreas y motivos; BDT los recibe en solo lectura por la API de integración.
