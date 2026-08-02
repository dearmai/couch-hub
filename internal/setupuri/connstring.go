package setupuri

import (
	"fmt"
	"net/url"
	"strings"
)

// This file reproduces ConnectionStringParser.serializeCouchDB/parseCouchDB and
// upsertRemoteConfigurationInPlace from livesync-commonlib.
//
// It exists because those functions derive settings.remoteConfigurations[id]
// from the live credentials - a random id plus a WHATWG-URL-encoded connection
// string - so unlike the rest of the settings object it cannot be baked into
// internal/setupuri/template.json at build time.
//
// The plugin treats that connection string as authoritative: on import,
// activateRemoteConfiguration() re-parses it and assigns the result back over
// couchDB_URI/USER/PASSWORD/DBNAME. Getting the encoding wrong therefore
// corrupts the credentials rather than failing loudly, which is exactly what
// scripts/verify-setup-uri.mjs guards against.

// proxyScheme is PROXY_SCHEME in ConnectionString.js: the placeholder scheme the
// connection string is built under before the "sls+<subscheme>:" swap.
const proxyScheme = "https"

// connectionString holds the pieces of a serialised CouchDB remote.
type connectionString struct {
	// URI is the "sls+http://user:pass@host/?db=vault" form stored in
	// remoteConfigurations[id].uri.
	URI string
	// Host is the normalised host[:port], used to name the configuration.
	Host string
	// NormalisedCouchDBURI is what the plugin will recover for couchDB_URI after
	// re-parsing URI. It differs from the operator-supplied address when a
	// trailing slash or a default port was given, and the *normalised* value is
	// what must go into the settings object so the two agree.
	NormalisedCouchDBURI string
}

// serializeCouchDB mirrors ConnectionStringParser.serializeCouchDB.
func serializeCouchDB(couchDBURI, user, password, dbName string) (connectionString, error) {
	u, err := url.Parse(strings.TrimSpace(couchDBURI))
	if err != nil {
		return connectionString{}, fmt.Errorf("setupuri: parse couchDB_URI: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" || u.Host == "" {
		return connectionString{}, fmt.Errorf("setupuri: couchDB_URI %q must be absolute (e.g. https://sync.example.com)", couchDBURI)
	}

	// `new URL(couchDB_URI)` normalises the host against the original scheme,
	// then `new URL(PROXY_SCHEME + "://" + url.host + ...)` normalises it again
	// against https. Both passes are reproduced, in that order, because the
	// second one can strip a :443 that the first one kept.
	host := normaliseHost(u.Host, scheme)
	host = normaliseHost(host, proxyScheme)

	path := u.Path
	if path == "" {
		path = "/"
	}

	// newUrl.username = encodeURIComponent(user) - the WHATWG userinfo setter
	// would percent-encode further, but encodeURIComponent's output contains
	// nothing from the userinfo encode set, so the setter is a no-op here.
	var userinfo string
	encUser, encPass := encodeURIComponent(user), encodeURIComponent(password)
	switch {
	case encUser == "" && encPass == "":
		userinfo = ""
	case encPass == "":
		userinfo = encUser + "@"
	default:
		userinfo = encUser + ":" + encPass + "@"
	}

	// searchParams.set("db", dbName) uses the form-urlencoded serialiser.
	query := "?db=" + encodeFormURLComponent(dbName)

	// withSlsScheme(): serialise under https, then replace the scheme.
	// out.slice(PROXY_SCHEME.length + 1) drops "https:" and keeps "//...".
	authorityAndRest := "//" + userinfo + host + path + query

	normalisedPath := path
	if normalisedPath == "/" {
		normalisedPath = ""
	}

	return connectionString{
		URI:                  "sls+" + scheme + ":" + authorityAndRest,
		Host:                 host,
		NormalisedCouchDBURI: scheme + "://" + host + normalisedPath,
	}, nil
}

// normaliseHost lowercases the hostname and drops the port when it is the
// default for scheme, matching WHATWG URL host serialisation.
func normaliseHost(host, scheme string) string {
	// Preserve IPv6 literals: only split on a colon after the closing bracket.
	hostPart, port := host, ""
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.Contains(host[idx:], "]") {
		hostPart, port = host[:idx], host[idx+1:]
	}
	hostPart = strings.ToLower(hostPart)

	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		return hostPart
	}
	return hostPart + ":" + port
}
