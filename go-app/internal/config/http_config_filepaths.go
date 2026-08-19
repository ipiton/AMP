package config

import (
	"path/filepath"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// resolveHTTPConfigFilepaths rewrites every RELATIVE file path inside an
// `http_config:` block to be relative to the directory holding the CONFIG FILE,
// exactly as upstream Alertmanager does in resolveFilepaths
// (alertmanager@v0.34.0/config/config.go:168):
//
//	join := func(fp string) string {
//	    if len(fp) > 0 && !filepath.IsAbs(fp) {
//	        fp = filepath.Join(baseDir, fp)
//	    }
//	    return fp
//	}
//
// Upstream applies that join to every `*_file` field it knows about, including
// the whole `http_config` family — `tls_config.ca_file`/`cert_file`/`key_file`,
// `basic_auth.password_file`, `authorization.credentials_file` and
// `oauth2.client_secret_file`. AMP read them relative to the PROCESS CWD, which
// is "/" in most containers, so the canonical upstream idiom
//
//	http_config:
//	  tls_config:
//	    ca_file: certs/internal-ca.pem
//
// resolved to /certs/internal-ca.pem and the target failed to build.
//
// SAME DEFECT CLASS as the `templates:` glob bug (see resolveTemplateGlobs on
// the templating track), but the failure MODE is the opposite and that is why
// this one is worth spelling out: an unmatched template glob loads clean and
// silently degrades formatting, whereas an unreadable ca_file is fail-CLOSED by
// design (FU-HTTP-CONFIG) — the target is skipped and stops delivering
// entirely, with an ERROR naming a path the operator never wrote. So the bug
// presented as "my relative ca_file is wrong" rather than as silence; loud, but
// still wrong, and it made a documented upstream config shape unusable.
//
// Applied at parse time, on the same `parsed` RouteConfig, so every consumer
// sees already-rebased paths: the config-target builder
// (business/publishing.httpClientConfigFor), the client builder
// (infrastructure/publishing.buildTLSConfig / resolveCredential) and the status
// API all read the same struct.
//
// Ordering: this runs AFTER infraroute.Parse(), which is what resolves
// `global.http_config` into each integration. That matters — the global block's
// own paths are rebased through the per-integration CLONES that inherited them,
// and `global.http_config` itself is rebased too so the status API and any
// future direct consumer agree.
//
// K8s Secret targets are deliberately NOT touched: their paths have no config
// file to be relative to (the "config" is a Secret key), so a relative path
// there keeps meaning "relative to the process CWD" — the only base that exists.
// Documented in docs/ALERTMANAGER_COMPATIBILITY.md.
//
// Absolute paths are left untouched, and so is the empty string (upstream's
// `len(fp) > 0` guard: filepath.Join(base, "") would silently turn "" into the
// base directory itself, i.e. invent a ca_file where the operator set none).
func resolveHTTPConfigFilepaths(rc *infraroute.RouteConfig, configPath string) {
	if rc == nil || configPath == "" {
		return
	}

	baseDir := filepath.Dir(configPath)

	if rc.Global != nil {
		rebaseHTTPConfigPaths(rc.Global.HTTPConfig, baseDir)
	}

	for _, receiver := range rc.Receivers {
		if receiver == nil {
			continue
		}
		for _, cfg := range receiver.WebhookConfigs {
			if cfg != nil {
				rebaseHTTPConfigPaths(cfg.HTTPConfig, baseDir)
			}
		}
		for _, cfg := range receiver.SlackConfigs {
			if cfg != nil {
				rebaseHTTPConfigPaths(cfg.HTTPConfig, baseDir)
			}
		}
		for _, cfg := range receiver.PagerDutyConfigs {
			if cfg != nil {
				rebaseHTTPConfigPaths(cfg.HTTPConfig, baseDir)
			}
		}
		for _, cfg := range receiver.TelegramConfigs {
			if cfg != nil {
				rebaseHTTPConfigPaths(cfg.HTTPConfig, baseDir)
			}
		}
	}
}

// rebaseHTTPConfigPaths joins every relative *_file path in hc onto baseDir.
// nil-safe; a nil block is the common case.
func rebaseHTTPConfigPaths(hc *infraroute.HTTPConfig, baseDir string) {
	if hc == nil {
		return
	}

	if tlsCfg := hc.TLSConfig; tlsCfg != nil {
		tlsCfg.CAFile = rebasePath(tlsCfg.CAFile, baseDir)
		tlsCfg.CertFile = rebasePath(tlsCfg.CertFile, baseDir)
		tlsCfg.KeyFile = rebasePath(tlsCfg.KeyFile, baseDir)
	}
	if basic := hc.BasicAuth; basic != nil {
		basic.PasswordFile = rebasePath(basic.PasswordFile, baseDir)
	}
	if authz := hc.Authorization; authz != nil {
		authz.CredentialsFile = rebasePath(authz.CredentialsFile, baseDir)
	}
	// oauth2 is not honoured (FU-HTTP-OAUTH2), but rebasing its path costs
	// nothing and means the field is already correct if it is ever wired —
	// rather than shipping a second copy of this bug then.
	if oauth := hc.OAuth2; oauth != nil {
		oauth.ClientSecretFile = rebasePath(oauth.ClientSecretFile, baseDir)
	}
}

// rebasePath is upstream's `join` closure: relative paths are resolved against
// baseDir, absolute paths and the empty string are returned unchanged.
func rebasePath(path, baseDir string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
