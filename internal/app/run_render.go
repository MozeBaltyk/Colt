package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
)

type podmanResource struct{ kind, name string }

func deploymentResources(name string) []podmanResource {
	return []podmanResource{
		{"network", name + "-net"},
		{"volume", name + "-data"},
		{"volume", name + "-config"},
		{"volume", name + "-db"},
	}
}

func deploymentContainers(name string) []string {
	return []string{name + "-app", name + "-db"}
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func parseDBPassword(data []byte, deploymentType string) (string, error) {
	if len(data) > 64<<10 {
		return "", errors.New("existing env file is too large")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		wanted := key == "DB_PASSWD"
		if deploymentType == "gitea" {
			wanted = wanted || key == "MARIADB_PASSWORD" || key == "MARIADB_ROOT_PASSWORD" || key == "GITEA__database__PASSWD"
		} else {
			wanted = wanted || key == "POSTGRES_PASSWORD" || key == "FORGEJO__database__PASSWD"
		}
		if wanted {
			if _, duplicate := values[key]; duplicate {
				return "", errors.New("existing env file has duplicate database password settings")
			}
			values[key] = value
		}
	}
	password := values["DB_PASSWD"]
	if !dbSecretRE.MatchString(password) {
		return "", errors.New("existing env file has an invalid database password")
	}
	keys := []string{"MARIADB_PASSWORD", "MARIADB_ROOT_PASSWORD", "GITEA__database__PASSWD"}
	if deploymentType == "forgejo" {
		keys = []string{"POSTGRES_PASSWORD", "FORGEJO__database__PASSWD"}
	}
	for _, key := range keys {
		if values[key] != password {
			return "", errors.New("existing env file has inconsistent database password settings")
		}
	}
	return password, nil
}

type runPaths struct {
	configDir, env, metadata, networkUnit, dbUnit, appUnit string
}

func deploymentPaths(name string) runPaths {
	return runPaths{
		configDir:   filepath.Join(runDir, name),
		env:         filepath.Join(runDir, name, "env"),
		metadata:    filepath.Join(runDir, name, "metadata"),
		networkUnit: filepath.Join(unitDir, "podman-network-"+name+"-net.service"),
		dbUnit:      filepath.Join(unitDir, "container-"+name+"-db.service"),
		appUnit:     filepath.Join(unitDir, "container-"+name+"-app.service"),
	}
}

func (p runPaths) all() []string {
	return []string{p.configDir, p.env, p.networkUnit, p.dbUnit, p.appUnit}
}

type renderedFile struct {
	path, content string
	mode          fs.FileMode
}

func renderDeploymentFiles(deploymentType, name, podman, image, appSecret, dbPassword, externalURL string) []renderedFile {
	p := deploymentPaths(name)
	appName := "Gitea"
	env := fmt.Sprintf("DB_TYPE=mysql\nDB_HOST=%s-db:3306\nDB_NAME=gitea\nDB_USER=gitea\nDB_PASSWD=%s\nMARIADB_DATABASE=gitea\nMARIADB_USER=gitea\nMARIADB_PASSWORD=%s\nMARIADB_ROOT_PASSWORD=%s\nGITEA__database__DB_TYPE=mysql\nGITEA__database__HOST=%s-db:3306\nGITEA__database__NAME=gitea\nGITEA__database__USER=gitea\nGITEA__database__PASSWD=%s\nGITEA__security__SECRET_KEY=%s\n", name, dbPassword, dbPassword, dbPassword, name, dbPassword, environmentValue(appSecret))
	dbDescription := "MariaDB"
	dbCommand := fmt.Sprintf("--env MARIADB_DATABASE --env MARIADB_USER --env MARIADB_PASSWORD --env MARIADB_ROOT_PASSWORD --volume %s-db:/var/lib/mysql --health-cmd \"healthcheck.sh --connect --innodb_initialized\" --health-interval 5s --health-retries 30 %s", name, mariaImage)
	appEnv := "--env GITEA__database__DB_TYPE --env GITEA__database__HOST --env GITEA__database__NAME --env GITEA__database__USER --env GITEA__database__PASSWD --env GITEA__security__SECRET_KEY"
	if deploymentType == "forgejo" {
		appName = "Forgejo"
		env = fmt.Sprintf("DB_TYPE=postgres\nDB_HOST=%s-db:5432\nDB_NAME=forgejo\nDB_USER=forgejo\nDB_PASSWD=%s\nPOSTGRES_DB=forgejo\nPOSTGRES_USER=forgejo\nPOSTGRES_PASSWORD=%s\nFORGEJO__database__DB_TYPE=postgres\nFORGEJO__database__HOST=%s-db:5432\nFORGEJO__database__NAME=forgejo\nFORGEJO__database__USER=forgejo\nFORGEJO__database__PASSWD=%s\nFORGEJO__security__SECRET_KEY=%s\nFORGEJO__server__SSH_PORT=2222\nFORGEJO__server__SSH_LISTEN_PORT=2222\n", name, dbPassword, dbPassword, name, dbPassword, environmentValue(appSecret))
		dbDescription = "PostgreSQL"
		dbCommand = fmt.Sprintf("--env POSTGRES_DB --env POSTGRES_USER --env POSTGRES_PASSWORD --volume %s-db:/var/lib/postgresql/data --health-cmd \"pg_isready -U forgejo -d forgejo\" --health-interval 5s --health-retries 30 %s", name, postgresImage)
		appEnv = "--env FORGEJO__database__DB_TYPE --env FORGEJO__database__HOST --env FORGEJO__database__NAME --env FORGEJO__database__USER --env FORGEJO__database__PASSWD --env FORGEJO__security__SECRET_KEY --env FORGEJO__server__SSH_PORT --env FORGEJO__server__SSH_LISTEN_PORT"
		// Official v16 rootless supports GITEA_APP_INI; persist config in its own volume.
		appEnv += " --env GITEA_APP_INI=/etc/gitea/app.ini"
	}
	if externalURL != "" {
		u, _ := url.Parse(externalURL)
		serverEnv := ""
		if deploymentType == "forgejo" {
			serverEnv = "--env FORGEJO__server__ROOT_URL --env FORGEJO__server__DOMAIN --env FORGEJO__server__SSH_DOMAIN --env FORGEJO__server__PROTOCOL"
			env += fmt.Sprintf("FORGEJO__server__ROOT_URL=%s\nFORGEJO__server__DOMAIN=%s\nFORGEJO__server__SSH_DOMAIN=%s\nFORGEJO__server__PROTOCOL=http\n",
				environmentValue(externalURL), environmentValue(u.Hostname()), environmentValue(u.Hostname()))
		} else {
			serverEnv = "--env GITEA__server__ROOT_URL --env GITEA__server__DOMAIN --env GITEA__server__SSH_DOMAIN --env GITEA__server__SSH_PORT --env GITEA__server__SSH_LISTEN_PORT --env GITEA__server__PROTOCOL"
			env += fmt.Sprintf("GITEA__server__ROOT_URL=%s\nGITEA__server__DOMAIN=%s\nGITEA__server__SSH_DOMAIN=%s\nGITEA__server__SSH_PORT=2222\nGITEA__server__SSH_LISTEN_PORT=2222\nGITEA__server__PROTOCOL=http\n",
				environmentValue(externalURL), environmentValue(u.Hostname()), environmentValue(u.Hostname()))
		}
		appEnv += " " + serverEnv
	}
	network := fmt.Sprintf(`[Unit]
Description=Podman network for Colt deployment %s

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s network exists %s-net

[Install]
WantedBy=multi-user.target
`, name, podman, name)
	db := fmt.Sprintf(`[Unit]
Description=%s for Colt deployment %s
Requires=podman-network-%s-net.service
After=podman-network-%s-net.service

[Service]
Restart=always
EnvironmentFile=%s
ExecStart=%s run --name %s-db --network %s-net %s
ExecStop=%s stop --time 10 %s-db

[Install]
WantedBy=multi-user.target
`, dbDescription, name, name, name, p.env, podman, name, name, dbCommand, podman, name)
	app := fmt.Sprintf(`[Unit]
Description=%s for Colt deployment %s
Requires=podman-network-%s-net.service container-%s-db.service
After=podman-network-%s-net.service container-%s-db.service

[Service]
Restart=always
TimeoutStartSec=300
EnvironmentFile=%s
ExecStartPre=/bin/sh -c 'until %s container exists %s-db; do sleep 1; done'
ExecStartPre=%s wait --condition=healthy %s-db
ExecStart=%s run --name %s-app --network %s-net %s --publish 127.0.0.1:3000:3000 --publish 2222:2222 --volume %s-data:/var/lib/gitea:U --volume %s-config:/etc/gitea:U %s
ExecStop=%s stop --time 10 %s-app

[Install]
WantedBy=multi-user.target
`, appName, name, name, name, name, name, p.env, podman, name, podman, name, podman, name, name, appEnv, name, name, image, podman, name)
	metadata := fmt.Sprintf("type=%s\nimage=%s\nexternal_url=%s\n", deploymentType, image, externalURL)
	return []renderedFile{{p.env, env, 0o600}, {p.metadata, metadata, 0o600}, {p.networkUnit, network, 0o644}, {p.dbUnit, db, 0o644}, {p.appUnit, app, 0o644}}
}

func environmentValue(value string) string {
	if plainEnvValueRE.MatchString(value) {
		return value
	}
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + value + `"`
}
