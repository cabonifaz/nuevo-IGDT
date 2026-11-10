-- Firma de cada usuario (imagen PNG) para las fichas de ingreso: la sube el propio usuario y se imprime sobre la línea
-- de firma de las fichas que confirma. El archivo no es público: solo se usa al imprimir y lo ve su dueño.
CREATE TABLE user_signature (
  user_id     BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  file_id     BIGINT UNSIGNED NOT NULL,
  updated_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  CONSTRAINT fk_user_signature_user FOREIGN KEY (user_id) REFERENCES app_user(id),
  CONSTRAINT fk_user_signature_file FOREIGN KEY (file_id) REFERENCES file_object(id)
) ENGINE=InnoDB;
