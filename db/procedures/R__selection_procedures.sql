-- =====================================================================
-- Bandeja de selección: requerimientos que BDT publica (copia de solo lectura con el avance de los postulantes) y
-- solicitudes de ingreso de los candidatos que Selección confirma. BDT los envía por la API de integración; los
-- gestores de servicios los consultan aquí. Los datos personales llegan ya cifrados por la API de IGDT.
-- =====================================================================
DELIMITER $$

-- ---------------- Requerimientos de BDT ----------------

-- Guarda el último estado de un RQ. Un envío más viejo que el guardado se ignora (applied = 0).
DROP PROCEDURE IF EXISTS sp_bdt_rq_upsert $$
CREATE PROCEDURE sp_bdt_rq_upsert(IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36), IN p_code VARCHAR(40), IN p_title VARCHAR(200),
  IN p_client_uuid CHAR(36), IN p_client_label VARCHAR(200), IN p_status_code VARCHAR(40), IN p_status_label VARCHAR(100),
  IN p_status_group VARCHAR(10), IN p_request_date DATE, IN p_due_date DATE, IN p_vacancies INT, IN p_covered INT, IN p_candidates INT,
  IN p_doc JSON, IN p_source_updated_at DATETIME(3), IN p_deleted TINYINT)
BEGIN
  DECLARE v_prev DATETIME(3);
  SET v_prev = (SELECT source_updated_at FROM bdt_requirement WHERE tenant_id = p_tenant_id AND uuid = p_uuid);
  IF v_prev IS NULL THEN
    INSERT INTO bdt_requirement (tenant_id, uuid, code, title, client_uuid, client_label, status_code, status_label, status_group,
                                 request_date, due_date, vacancies, covered, candidates, doc, source_updated_at, deleted_at)
    VALUES (p_tenant_id, p_uuid, p_code, p_title, p_client_uuid, p_client_label, p_status_code, p_status_label, p_status_group,
            p_request_date, p_due_date, p_vacancies, p_covered, p_candidates, p_doc, p_source_updated_at, IF(p_deleted = 1, NOW(3), NULL));
    SELECT 1 AS applied;
  ELSEIF p_source_updated_at >= v_prev THEN
    UPDATE bdt_requirement SET code = p_code, title = p_title, client_uuid = p_client_uuid, client_label = p_client_label,
           status_code = p_status_code, status_label = p_status_label, status_group = p_status_group, request_date = p_request_date,
           due_date = p_due_date, vacancies = p_vacancies, covered = p_covered, candidates = p_candidates, doc = p_doc,
           source_updated_at = p_source_updated_at, received_at = NOW(3),
           deleted_at = IF(p_deleted = 1, IFNULL(deleted_at, NOW(3)), NULL)
     WHERE tenant_id = p_tenant_id AND uuid = p_uuid;
    SELECT 1 AS applied;
  ELSE
    SELECT 0 AS applied;
  END IF;
END $$

-- Bandeja: p_scope OPEN | CLOSED | ALL. Abiertos primero, por fecha límite.
DROP PROCEDURE IF EXISTS sp_bdt_rq_list $$
CREATE PROCEDURE sp_bdt_rq_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_scope VARCHAR(10))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('id', r.uuid, 'code', r.code, 'title', r.title,
           'status', JSON_OBJECT('code', r.status_code, 'label', r.status_label, 'group', r.status_group),
           'client', JSON_OBJECT('id', r.client_uuid, 'label', r.client_label),
           'requestDate', r.request_date, 'dueDate', r.due_date, 'vacancies', r.vacancies, 'covered', r.covered,
           'candidates', r.candidates, 'stageCounts', JSON_EXTRACT(r.doc, '$.stageCounts'),
           'owner', JSON_EXTRACT(r.doc, '$.owner'), 'closedAt', JSON_EXTRACT(r.doc, '$.closedAt'),
           'createdAt', JSON_EXTRACT(r.doc, '$.createdAt'), 'alerts', JSON_EXTRACT(r.doc, '$.alerts'),
           'inProcessAt', JSON_EXTRACT(r.doc, '$.inProcessAt'),
           'profiles', (SELECT JSON_ARRAYAGG(JSON_OBJECT('id', v.pid, 'label', v.label, 'total', v.qty, 'covered', IFNULL(v.cov, 0)))
                          FROM JSON_TABLE(r.doc, '$.vacancies[*]' COLUMNS (pid CHAR(36) PATH '$.profile.id', label VARCHAR(200) PATH '$.profile.label',
                                                                         qty INT PATH '$.quantity', cov INT PATH '$.covered')) v),
           'hiresOpen', (SELECT COUNT(*) FROM hire_request h WHERE h.tenant_id = r.tenant_id AND h.requirement_uuid = r.uuid
                          AND h.status IN ('PENDING', 'IN_PROGRESS')),
           'updatedAt', r.source_updated_at, 'receivedAt', r.received_at) AS doc
    FROM bdt_requirement r
   WHERE r.tenant_id = p_tenant_id AND r.deleted_at IS NULL
     AND (p_scope = 'ALL' OR r.status_group = p_scope)
   ORDER BY r.status_group = 'OPEN' DESC, r.due_date, r.code
   LIMIT 1000;
END $$

-- Detalle del RQ: snapshot completo (vacantes y postulantes) con las solicitudes de ingreso que generó.
DROP PROCEDURE IF EXISTS sp_bdt_rq_get $$
CREATE PROCEDURE sp_bdt_rq_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  IF NOT EXISTS (SELECT 1 FROM bdt_requirement WHERE tenant_id = p_tenant_id AND uuid = p_uuid AND deleted_at IS NULL) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND';
  END IF;
  SELECT JSON_SET(r.doc, '$.receivedAt', r.received_at, '$.updatedAt', r.source_updated_at,
           '$.hires', IFNULL((SELECT JSON_ARRAYAGG(JSON_OBJECT('id', h.uuid, 'candidateId', h.candidate_uuid, 'status', h.status))
                                FROM hire_request h WHERE h.tenant_id = r.tenant_id AND h.requirement_uuid = r.uuid), JSON_ARRAY())) AS doc
    FROM bdt_requirement r
   WHERE r.tenant_id = p_tenant_id AND r.uuid = p_uuid;
END $$

-- ---------------- Solicitudes de ingreso ----------------

-- Recibe el estado de un candidato de BDT. p_active = 1: confirmado (crea, actualiza o reabre la solicitud);
-- 0: ya no está confirmado (cancela la solicitud si aún no se completó). Devuelve el evento para notificar:
-- NEW | REOPENED | UPDATED | CANCELLED | NONE.
DROP PROCEDURE IF EXISTS sp_hire_request_receive $$
CREATE PROCEDURE sp_hire_request_receive(IN p_tenant_id BIGINT UNSIGNED, IN p_api_key_id BIGINT UNSIGNED, IN p_new_uuid CHAR(36),
  IN p_candidate_uuid CHAR(36), IN p_active TINYINT, IN p_requirement_uuid CHAR(36), IN p_talent_uuid CHAR(36),
  IN p_first VARCHAR(100), IN p_last VARCHAR(100), IN p_second VARCHAR(100), IN p_headline VARCHAR(200),
  IN p_doc_type_code VARCHAR(40), IN p_doc_type_label VARCHAR(100), IN p_doc_number_enc VARBINARY(512), IN p_doc_number_bidx CHAR(64),
  IN p_email_enc VARBINARY(1024), IN p_phone_enc VARBINARY(512), IN p_client_uuid CHAR(36), IN p_client_label VARCHAR(200),
  IN p_profile_uuid CHAR(36), IN p_profile_label VARCHAR(200), IN p_doc JSON, IN p_confirmed_at DATETIME(3))
BEGIN
  DECLARE v_id BIGINT UNSIGNED;
  DECLARE v_uuid CHAR(36);
  DECLARE v_status VARCHAR(12);
  DECLARE v_event VARCHAR(10) DEFAULT 'NONE';
  SELECT id, uuid, status INTO v_id, v_uuid, v_status FROM hire_request WHERE tenant_id = p_tenant_id AND candidate_uuid = p_candidate_uuid;
  IF p_active = 1 THEN
    IF v_id IS NULL THEN
      SET v_uuid = p_new_uuid, v_event = 'NEW';
      INSERT INTO hire_request (uuid, tenant_id, candidate_uuid, requirement_uuid, talent_uuid, first_name, last_name, second_last_name,
                                headline, doc_type_code, doc_type_label, doc_number_enc, doc_number_bidx, email_enc, phone_enc,
                                client_uuid, client_label, profile_uuid, profile_label, doc, confirmed_at)
      VALUES (v_uuid, p_tenant_id, p_candidate_uuid, p_requirement_uuid, p_talent_uuid, p_first, p_last, p_second, p_headline,
              p_doc_type_code, p_doc_type_label, p_doc_number_enc, p_doc_number_bidx, p_email_enc, p_phone_enc,
              p_client_uuid, p_client_label, p_profile_uuid, p_profile_label, p_doc, p_confirmed_at);
    ELSEIF v_status IN ('COMPLETED', 'REVOKED') THEN
      SET v_event = 'NONE'; -- el ingreso ya se registró: el snapshot queda como estaba
    ELSE
      SET v_event = IF(v_status = 'CANCELLED', 'REOPENED', 'UPDATED');
      UPDATE hire_request SET requirement_uuid = p_requirement_uuid, talent_uuid = p_talent_uuid, first_name = p_first, last_name = p_last,
             second_last_name = p_second, headline = p_headline, doc_type_code = p_doc_type_code, doc_type_label = p_doc_type_label,
             doc_number_enc = p_doc_number_enc, doc_number_bidx = p_doc_number_bidx, email_enc = p_email_enc, phone_enc = p_phone_enc,
             client_uuid = p_client_uuid, client_label = p_client_label, profile_uuid = p_profile_uuid, profile_label = p_profile_label,
             doc = p_doc, confirmed_at = p_confirmed_at,
             status = IF(v_status = 'CANCELLED', 'PENDING', status),
             received_at = IF(v_status = 'CANCELLED', NOW(3), received_at),
             cancelled_at = NULL,
             assigned_user_id = IF(v_status = 'CANCELLED', NULL, assigned_user_id),
             taken_at = IF(v_status = 'CANCELLED', NULL, taken_at)
       WHERE id = v_id;
    END IF;
  ELSEIF v_id IS NOT NULL AND v_status IN ('PENDING', 'IN_PROGRESS') THEN
    SET v_event = 'CANCELLED';
    UPDATE hire_request SET status = 'CANCELLED', cancelled_at = NOW(3) WHERE id = v_id;
  END IF;
  IF v_event IN ('NEW', 'REOPENED', 'CANCELLED') THEN
    CALL sp_audit_insert(p_tenant_id, NULL, p_api_key_id, CONCAT('HIRE_REQUEST_', v_event), 'hire_request', v_uuid,
                         JSON_OBJECT('candidate', p_candidate_uuid, 'requirement', p_requirement_uuid), NULL);
  END IF;
  SELECT v_uuid AS uuid, v_event AS event, (SELECT assigned_user_id FROM hire_request WHERE tenant_id = p_tenant_id AND candidate_uuid = p_candidate_uuid) AS assigned;
END $$

-- Bandeja de solicitudes: p_status NULL = todas. Pendientes primero; las más recientes arriba.
DROP PROCEDURE IF EXISTS sp_hire_request_list $$
CREATE PROCEDURE sp_hire_request_list(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_status VARCHAR(12))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  SELECT JSON_OBJECT('id', h.uuid, 'status', h.status, 'candidateId', h.candidate_uuid,
           'talentId', h.talent_uuid, 'requirement', JSON_OBJECT('id', h.requirement_uuid, 'code', JSON_EXTRACT(h.doc, '$.requirement.code'),
                                      'title', JSON_EXTRACT(h.doc, '$.requirement.title')),
           'talent', JSON_OBJECT('firstName', h.first_name, 'lastName', h.last_name, 'secondLastName', h.second_last_name,
                                 'headline', h.headline, 'docType', h.doc_type_label),
           'client', JSON_OBJECT('id', h.client_uuid, 'label', h.client_label),
           'profile', JSON_OBJECT('id', h.profile_uuid, 'label', h.profile_label),
           'confirmedAt', h.confirmed_at, 'receivedAt', h.received_at, 'takenAt', h.taken_at, 'cancelledAt', h.cancelled_at,
           'completedAt', h.completed_at, 'revokedAt', h.revoked_at, 'hasDraft', h.draft IS NOT NULL, 'assignedNameEnc', TO_BASE64(u.full_name_enc), 'mine', h.assigned_user_id <=> p_actor_id) AS doc
    FROM hire_request h
    LEFT JOIN app_user u ON u.id = h.assigned_user_id
   WHERE h.tenant_id = p_tenant_id AND (p_status IS NULL OR h.status = p_status)
   ORDER BY FIELD(h.status, 'PENDING', 'IN_PROGRESS', 'CANCELLED', 'COMPLETED', 'REVOKED'), h.received_at DESC
   LIMIT 1000;
END $$

-- Detalle con los datos personales (cifrados; la API los descifra). Cada consulta queda auditada.
DROP PROCEDURE IF EXISTS sp_hire_request_get $$
CREATE PROCEDURE sp_hire_request_get(IN p_actor_id BIGINT UNSIGNED, IN p_tenant_id BIGINT UNSIGNED, IN p_uuid CHAR(36), IN p_ip VARCHAR(45))
BEGIN
  CALL sp_assert_actor(p_actor_id, p_tenant_id, 'hire.read');
  IF NOT EXISTS (SELECT 1 FROM hire_request WHERE tenant_id = p_tenant_id AND uuid = p_uuid) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'IGDT:NOT_FOUND';
  END IF;
  CALL sp_audit_insert(p_tenant_id, p_actor_id, NULL, 'VIEW', 'hire_request', p_uuid, NULL, p_ip);
  SELECT JSON_OBJECT('id', h.uuid, 'status', h.status, 'candidateId', h.candidate_uuid, 'talentId', h.talent_uuid,
           'requirement', JSON_MERGE_PATCH(JSON_OBJECT('id', h.requirement_uuid), IFNULL(JSON_EXTRACT(h.doc, '$.requirement'), JSON_OBJECT())),
           'talent', JSON_OBJECT('firstName', h.first_name, 'lastName', h.last_name, 'secondLastName', h.second_last_name,
                                 'headline', h.headline, 'docType', JSON_OBJECT('code', h.doc_type_code, 'label', h.doc_type_label),
                                 'docNumberEnc', TO_BASE64(h.doc_number_enc), 'emailEnc', TO_BASE64(h.email_enc),
                                 'phoneEnc', TO_BASE64(h.phone_enc)),
           'client', JSON_OBJECT('id', h.client_uuid, 'label', h.client_label),
           'profile', JSON_OBJECT('id', h.profile_uuid, 'label', h.profile_label),
           'vacancy', JSON_EXTRACT(h.doc, '$.vacancy'), 'conditions', JSON_EXTRACT(h.doc, '$.conditions'),
           'confirmedBy', JSON_EXTRACT(h.doc, '$.confirmedBy'),
           'confirmedAt', h.confirmed_at, 'receivedAt', h.received_at, 'takenAt', h.taken_at, 'cancelledAt', h.cancelled_at,
           'completedAt', h.completed_at, 'revokedAt', h.revoked_at, 'assignedNameEnc', TO_BASE64(u.full_name_enc),
           'mine', h.assigned_user_id <=> p_actor_id) AS doc
    FROM hire_request h
    LEFT JOIN app_user u ON u.id = h.assigned_user_id
   WHERE h.tenant_id = p_tenant_id AND h.uuid = p_uuid;
END $$

-- Un gestor toma la solicitud (queda en curso a su cargo). Se puede reasignar mientras no esté cerrada.
DELIMITER ;
