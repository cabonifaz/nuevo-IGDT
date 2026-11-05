# IGDT — Ingreso y Gestión de Talentos (backend)

Backend en Go con arquitectura hexagonal (`internal/app` casos de uso, `ports`, `adapters`). Toda la regla de negocio y
el acceso a datos están en **stored procedures de MySQL** (`db/procedures`, `R__` repetibles) y el esquema en migraciones
versionadas (`db/migrations`, `V00xx`, nunca se editan una vez aplicadas). El backend no contiene sentencias SQL.

IGDT es un sistema aislado de BDT (Banco de Talentos): tiene su propia base de datos y se comunica con BDT solo por API,
con tokens de integración por empresa (alcances mínimos, rotables, auditados e idempotentes).

## Ejecutar en local

1. Copia las variables de entorno a `.env` (nunca se sube al repo; ver `internal/platform/config/config.go`).
2. `go run ./cmd/migrate` — aplica migraciones y procedimientos con el usuario migrador.
3. `BOOTSTRAP_EMAIL=... BOOTSTRAP_NAME=... BOOTSTRAP_PASSWORD=... go run ./cmd/bootstrap` — primer Super Admin.
4. `go run ./cmd/api` — API en `HTTP_PORT` (8081 en local).

## Seguridad

- Ninguna clave en el repositorio: todo por variables de entorno.
- Usuario de la app con permiso solo de `EXECUTE` sobre los procedimientos; migraciones con un usuario aparte.
- Sesión: access token corto + refresh en cookie HttpOnly con rotación y detección de reuso.
- Datos personales cifrados (AES-GCM) con índice ciego para búsquedas; contraseñas con Argon2id.
