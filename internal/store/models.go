package store

import "time"

// Profile is a CouchDB server CouchHub manages or connects to.
//
// The two addresses are deliberately separate. AdminBaseURL is how CouchHub
// reaches the server - typically a container-network address that is not
// routable from outside. PublicBaseURL is what goes into a vault's Setup URI,
// because Obsidian on a phone reaches CouchDB through the reverse proxy.
// Conflating them produces Setup URIs that work on the host and nowhere else.
type Profile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AdminBaseURL  string `json:"adminBaseUrl"`
	PublicBaseURL string `json:"publicBaseUrl"`
	AdminUser     string `json:"adminUser"`

	// AdminPasswordSealed is nil when the store is running without
	// COUCHHUB_SECRET; see Vault.SecretsPersisted.
	AdminPasswordSealed []byte `json:"adminPasswordSealed,omitempty"`

	// Provisioned records that the livesync configuration was applied
	// successfully at least once.
	Provisioned bool `json:"provisioned"`

	// Primary marks the server new vaults land on when none is named. Exactly
	// one profile carries it; the store has no way to enforce that, so the
	// handlers clear it on the others when it moves.
	Primary bool `json:"primary"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Vault is one Obsidian vault, backed by one CouchDB database and one
// dedicated CouchDB account.
type Vault struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId"`
	Name      string `json:"name"`
	DBName    string `json:"dbName"`
	CouchUser string `json:"couchUser"`

	CouchPasswordSealed  []byte `json:"couchPasswordSealed,omitempty"`
	E2EEPassphraseSealed []byte `json:"e2eePassphraseSealed,omitempty"`
	SetupPINSealed       []byte `json:"setupPinSealed,omitempty"`

	// SetupPINExpiresAt is when the current PIN stops being valid.
	//
	// The Setup URI is encrypted under the PIN and nothing phones home to
	// redeem it, so the only way to expire a code that has been shown is to
	// replace the PIN it was encrypted with. A background sweep does exactly
	// that once this passes, which is what makes the displayed code
	// short-lived even if the browser showing it is closed or killed.
	//
	// Zero means the PIN does not expire, which is every vault issued before
	// this existed.
	SetupPINExpiresAt time.Time `json:"setupPinExpiresAt,omitzero"`

	// SecretsPersisted is false when the vault was created without
	// COUCHHUB_SECRET set. Its credentials were shown once and are gone; the UI
	// must not offer to reissue a Setup URI for it.
	SecretsPersisted bool `json:"secretsPersisted"`

	// Adopted marks a database CouchHub did not create. Deleting one defaults to
	// detaching rather than dropping, because the data predates CouchHub.
	Adopted bool `json:"adopted"`

	// E2EEDisabled records a vault whose contents are stored unencrypted. Only
	// reachable by adopting a database that was already set up that way - the
	// passphrase is what decrypts existing chunks, so it cannot be introduced
	// afterwards.
	E2EEDisabled bool `json:"e2eeDisabled"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Migration is a vault being moved from one CouchDB server to another.
//
// The copy itself is done by CouchDB: CouchHub writes a one-shot document into
// the target's _replicator and then only watches it. That is what lets the move
// survive a CouchHub restart - the record here is enough to pick the job back up
// and finish it.
//
// It is keyed by vault, so a vault has at most one move in flight.
type Migration struct {
	VaultID         string `json:"vaultId"`
	SourceProfileID string `json:"sourceProfileId"`
	TargetProfileID string `json:"targetProfileId"`
	DBName          string `json:"dbName"`

	// ReplicationID names the document in the target's _replicator.
	ReplicationID string `json:"replicationId"`

	// DeleteSource drops the original database once the copy has finished.
	// Cleared by default: a copy that is verified afterwards is recoverable, a
	// deleted database is not.
	DeleteSource bool `json:"deleteSource"`

	StartedAt time.Time `json:"startedAt"`
}

// Snapshot is one poll of a vault's CouchDB statistics, from GET /{db}.
type Snapshot struct {
	VaultID string    `json:"vaultId"`
	At      time.Time `json:"at"`

	DocCount    int64 `json:"docCount"`
	DocDelCount int64 `json:"docDelCount"`

	SizeFile     int64 `json:"sizeFile"`
	SizeActive   int64 `json:"sizeActive"`
	SizeExternal int64 `json:"sizeExternal"`

	// UpdateSeqNum is the numeric prefix of CouchDB's update_seq. Its delta
	// between polls is what feeds the activity heatmap: counting writes this way
	// costs one request, whereas walking _changes would cost one per document.
	UpdateSeqNum int64 `json:"updateSeqNum"`
}

// ActivityDay is one cell of the contribution-style heatmap.
type ActivityDay struct {
	Day    string `json:"day"` // YYYY-MM-DD, UTC
	Writes uint32 `json:"writes"`
}
