-- Formato de las fichas de ingreso por tenant (FT-GTH-12, FT-GS-03, FT-GS-01): colores, logo, códigos/versión/fecha de
-- cada formato, cargo del firmante y texto del pie. Lo configuran el administrador del tenant y el Super Admin.
-- style NULL = formato por defecto (el de las fichas originales del FMI).
CREATE TABLE hire_form_style (
  tenant_id     BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  style         JSON            NULL,
  logo_file_id  BIGINT UNSIGNED NULL,      -- logo propio de las fichas (PNG/JPG); sin él se usa el logo de la marca
  updated_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  updated_by    BIGINT UNSIGNED NULL,
  CONSTRAINT fk_hire_form_style_tenant FOREIGN KEY (tenant_id) REFERENCES tenant(id),
  CONSTRAINT fk_hire_form_style_logo FOREIGN KEY (logo_file_id) REFERENCES file_object(id),
  CONSTRAINT ck_hire_form_style_json CHECK (style IS NULL OR JSON_TYPE(style) = 'OBJECT')
) ENGINE=InnoDB;
