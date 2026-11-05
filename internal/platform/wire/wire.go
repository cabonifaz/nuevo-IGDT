// Package wire arma la aplicación: conecta adaptadores con casos de uso (composition root).
package wire

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/httpapi"
	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/mailer"
	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/mysql"
	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/push"
	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/security"
	"github.com/cabonifaz/nuevo-IGDT/internal/adapters/storage"
	"github.com/cabonifaz/nuevo-IGDT/internal/app"
	"github.com/cabonifaz/nuevo-IGDT/internal/platform/config"
	"github.com/cabonifaz/nuevo-IGDT/internal/platform/release"
)

type App struct {
	DB     *sql.DB
	Auth   *app.AuthService
	Server *httpapi.Server
}

func Build(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	db, err := mysql.Open(ctx, mysql.ConnConfig{Host: cfg.DBHost, Port: cfg.DBPort, Name: cfg.DBName,
		User: cfg.DBUser, Password: cfg.DBPassword, TLS: cfg.DBTLS})
	if err != nil {
		return nil, err
	}
	cipher, err := security.NewAESCipher(cfg.DataKey, cfg.BlindIndexKey)
	if err != nil {
		return nil, err
	}
	issuer, err := security.NewJWTIssuer(cfg.JWTSecret, cfg.AccessTTL)
	if err != nil {
		return nil, err
	}
	files, err := storage.NewLocal(cfg.StoragePath)
	if err != nil {
		return nil, err
	}
	store := mysql.NewStore(db)
	// zona horaria por defecto de los tenants
	if _, err := time.LoadLocation(cfg.DefaultTimezone); err != nil {
		return nil, err
	}
	hasher := security.NewArgon2Hasher()
	tenantRepo := mysql.TenantRepo{Store: store}

	auth := &app.AuthService{Repo: mysql.AuthRepo{Store: store}, Cipher: cipher, Hasher: hasher, Tokens: issuer,
		Cfg: app.AuthConfig{RefreshTTL: cfg.RefreshTTL, RememberTTL: cfg.RememberTTL, ReuseGrace: cfg.ReuseGrace, MaxAttempts: 5, LockMinutes: 15}}
	users := &app.UserService{Users: mysql.UserRepo{Store: store}, Invitations: mysql.InvitationRepo{Store: store},
		Cipher: cipher, Hasher: hasher, Mailer: mailer.LogMailer{Log: log}, AppURL: cfg.PublicAppURL,
		InviteTTL: cfg.InviteTTL, ExposeLink: cfg.Env == "local" || cfg.Env == "staging"}
	fileRepo := mysql.FileRepo{Store: store}

	notifications := &app.NotificationService{Repo: mysql.NotificationRepo{Store: store}, Cipher: cipher,
		Push: push.New(cfg.VAPIDPublic, cfg.VAPIDPrivate, cfg.VAPIDSubject)}
	// catálogo de funcionalidades: habilita los módulos por tenant y avisa de las nuevas al Super Admin
	features := &app.FeatureService{Repo: mysql.FeatureRepo{Store: store}, Notifier: notifications}
	go features.AnnounceNew(context.Background())
	// versión del despliegue: se registra en el ambiente y, si es nueva, se avisa a todos
	versions := &app.VersionService{Repo: mysql.VersionRepo{Store: store}, Notifier: notifications, Env: cfg.Env,
		Version: release.Version(), Commit: cfg.CommitSHA, Notes: release.Notes(release.Version()), Peers: app.ParsePeers(cfg.EnvURLs)}
	go versions.Register(context.Background())
	srv := httpapi.NewServer(httpapi.Config{CORSOrigins: cfg.CORSOrigins, TrustProxy: cfg.TrustProxy,
		CookieSecure: !cfg.IsLocal(), RateGeneral: cfg.RateGeneralPerMin, RateAuth: cfg.RateAuthPerMin}, log, httpapi.Deps{
		Auth:          auth,
		Tenants:       &app.TenantService{Repo: tenantRepo, Users: users},
		Users:         users,
		ApiKeys:       &app.ApiKeyService{Repo: mysql.ApiKeyRepo{Store: store}, Pepper: cfg.APIKeyPepper},
		Masters:       &app.MasterService{Repo: mysql.MasterRepo{Store: store}},
		Files:         &app.FileService{Repo: fileRepo, Storage: files, Tenants: tenantRepo},
		Notifications: notifications,
		Features:      features,
		Versions:      versions,
		Support:       &app.SupportService{Repo: mysql.SupportRepo{Store: store}, Cipher: cipher, Storage: files, Notifier: notifications},
		Sync:          mysql.SyncRepo{Store: store},
		Audit:         mysql.AuditRepo{Store: store},
		Translations:  mysql.TranslationRepo{Store: store},
		Tokens:        issuer,
		Health:        func() error { return store.Ping(context.Background()) },
	})
	return &App{DB: db, Auth: auth, Server: srv}, nil
}
