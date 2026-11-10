# Registro de versiones de IGDT (Ingreso y Gestión de Talentos)

Formato: `## vMAYOR.MENOR — AAAA-MM-DD`, seguido de los cambios en viñetas (español). Al publicar una versión se
sube `VERSION` y se agrega su sección aquí: la API la registra al desplegarse en cada ambiente y la notifica.

## v0.5 — 2026-11-10
- Perfil completo del candidato leído de BDT: contacto, experiencia, estudios, habilidades, idiomas, evaluaciones, CV en español e inglés (con la marca de la empresa) y CV original, y sus postulaciones a otros requerimientos. Se abre con «Ver perfil» en la solicitud o desde el pipeline del RQ.
- Fichas de ingreso en la solicitud: FT-GTH-12 Formulario de Ingreso, FT-GS-03 Solicitud de Hardware y Software (con procesador, RAM, disco, anexo, celular, accesorios y software) y FT-GS-01 Solicitud de Creación de Usuarios. Llegan propuestas, se guardan como borrador, se ven en PDF y al confirmarlas se registra el ingreso y cada ficha se envía a sus destinatarios.
- Corrección y revocación del ingreso confirmado hasta la fecha de inicio más los días de gracia: historial de cambios, motivo obligatorio al cambiar la fecha de inicio y aviso a BDT del cambio de inicio o de la revocación. Nueva columna Revocada en las solicitudes cerradas.
- Administración › Fichas de ingreso: destinatarios de cada ficha, copia al gestor, días de gracia y dominio de correo corporativo.

## v0.4 — 2026-11-09
- Requerimientos en tablero Kanban, igual que en BDT: columnas por estado (Registrado, Asignado, En selección, En producción y Cerrados), vistas Abiertos, Por vencer, Vencidos, Nuevos, Sin movimiento, Cerrados y Todos, filtros por cliente y perfil, cobertura por perfil y responsables. La ficha tiene las pestañas Candidatos y Detalle. Es de solo lectura.
- Solicitudes de ingreso en tablero Kanban: Pendiente, En curso y Cerradas. Se toman arrastrándolas a En curso y el ingreso se registra al soltarlas en Cerradas. Vistas Abiertas, Nuevas, Sin movimiento, Cerradas y Todas, con filtro «Solo mías».

## v0.3 — 2026-11-09
- Registro del ingreso desde la solicitud: contrato (cargo, modalidad, motivo, inicio y término propuesto por el RQ), área, sede, ubicación, horario, remuneración (planilla o locación de servicios, guardada cifrada), SUNAT y equipos a entregar.
- Colaboradores: los talentos con ingreso registrado y su contrato vigente, con aviso de los que vencen en 30 días.
- BDT se entera del ingreso al momento (evento de ingreso con el alcance `hires.read`): marca al candidato y cierra el requerimiento cuando cubre sus vacantes.

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
