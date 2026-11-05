-- Plantilla de los documentos PDF de la empresa (formularios de ingreso, movimiento y cese): imagen de cabecera y de
-- pie, texto del pie. Los nombres de columna vienen de la plantilla de CV de BDT (misma estructura de marca).
ALTER TABLE tenant_branding
  ADD COLUMN cv_header_file_id BIGINT UNSIGNED NULL AFTER favicon_file_id,
  ADD COLUMN cv_footer_file_id BIGINT UNSIGNED NULL AFTER cv_header_file_id,
  ADD COLUMN cv_footer_text    VARCHAR(300) NULL AFTER cv_footer_file_id,
  ADD COLUMN cv_show_photo     TINYINT(1) NOT NULL DEFAULT 0 AFTER cv_footer_text,
  ADD CONSTRAINT fk_branding_cv_header FOREIGN KEY (cv_header_file_id) REFERENCES file_object(id),
  ADD CONSTRAINT fk_branding_cv_footer FOREIGN KEY (cv_footer_file_id) REFERENCES file_object(id);
