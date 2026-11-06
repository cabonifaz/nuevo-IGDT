-- La marca inicial de Fractal usaba el logo del Banco de Talentos (dice «banco de talentos»): sin logo se muestra el
-- nombre de la empresa hasta que el admin suba el de IGDT en Administración › Marca.
UPDATE tenant_branding SET logo_url = NULL, display_name = 'Fractal'
 WHERE logo_url = '/tenants/fractal/logo.png' AND logo_file_id IS NULL;
