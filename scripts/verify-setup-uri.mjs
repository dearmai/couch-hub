// Cross-checks internal/setupuri against the real obsidian-livesync library.
//
// This is the acceptance gate for M1. The Go code reimplements livesync's
// encryption and connection-string encoding; if either drifts, Obsidian rejects
// the Setup URI - or worse, imports subtly wrong credentials. So we check both
// directions against the published package:
//
//   forward  Go writes a Setup URI  -> official decodeSettingsFromSetupURI reads it
//   reverse  official encodeSettingsToSetupURI writes one -> Go's Parse reads it
//   shape    Go's settings object matches what the official generator builds
//
// Run with: make verify-uri     (dev/CI only - never shipped in the image)

import { execFileSync } from "node:child_process";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

import {
    encodeSettingsToSetupURI,
    decodeSettingsFromSetupURI,
    encodeSettingsToQRCodeData,
    decodeSettingsFromQRCodeData,
} from "@vrtmrz/livesync-commonlib/compat/API/processSetting";
import { createNewVaultSettings } from "@vrtmrz/livesync-commonlib/settings";
import { upsertRemoteConfigurationInPlace } from "@vrtmrz/livesync-commonlib/remote-configurations";
import {
    DEFAULT_SETTINGS,
    PREFERRED_SETTING_SELF_HOSTED,
} from "@vrtmrz/livesync-commonlib/compat/common/types";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// Values that legitimately differ per run and cannot be compared literally:
// the remote configuration id embeds Date.now() and Math.random().
const VOLATILE = new Set(["remoteConfigurations", "activeConfigurationId"]);

const CASES = [
    {
        name: "plain host and port",
        couchDB_URI: "http://192.168.1.50:5984",
        couchDB_USER: "vault_notes",
        couchDB_PASSWORD: "s3cret-pw",
        couchDB_DBNAME: "notes",
        passphrase: "vault-e2ee-passphrase",
        uriPassphrase: "482917",
    },
    {
        name: "https behind reverse proxy",
        couchDB_URI: "https://sync.example.com",
        couchDB_USER: "vault_work",
        couchDB_PASSWORD: "another-pw",
        couchDB_DBNAME: "work_vault",
        passphrase: "different-passphrase",
        uriPassphrase: "",
    },
    {
        name: "trailing slash is normalised away",
        couchDB_URI: "https://sync.example.com/",
        couchDB_USER: "u",
        couchDB_PASSWORD: "p",
        couchDB_DBNAME: "v",
        passphrase: "pp",
        uriPassphrase: "000000",
    },
    {
        name: "subpath mount",
        couchDB_URI: "https://example.com/couchdb",
        couchDB_USER: "u",
        couchDB_PASSWORD: "p",
        couchDB_DBNAME: "v",
        passphrase: "pp",
        uriPassphrase: "123456",
    },
    {
        name: "default port is stripped",
        couchDB_URI: "https://sync.example.com:443",
        couchDB_USER: "u",
        couchDB_PASSWORD: "p",
        couchDB_DBNAME: "v",
        passphrase: "pp",
        uriPassphrase: "123456",
    },
    {
        name: "uppercase host is lowercased",
        couchDB_URI: "https://SYNC.Example.COM:5984",
        couchDB_USER: "u",
        couchDB_PASSWORD: "p",
        couchDB_DBNAME: "v",
        passphrase: "pp",
        uriPassphrase: "123456",
    },
    {
        name: "credentials needing percent-encoding",
        couchDB_URI: "http://couch.local:5984",
        // ':' '@' '/' '#' '?' '%' '&' '=' '+' ' ' all have to survive the
        // userinfo round-trip through the connection string.
        couchDB_USER: "user:with@weird/chars",
        couchDB_PASSWORD: "p@ss:w#rd?&=+% ok",
        couchDB_DBNAME: "vault",
        passphrase: "pass phrase with spaces",
        uriPassphrase: "9 9",
    },
    {
        name: "couchdb-legal db name punctuation",
        couchDB_URI: "http://couch.local:5984",
        couchDB_USER: "u",
        couchDB_PASSWORD: "p",
        // CouchDB allows [a-z][a-z0-9_$()+/-]* - the form-urlencoded serialiser
        // must escape '+', '/', '(', ')' and '$'.
        couchDB_DBNAME: "vault_$()+/-name",
        passphrase: "pp",
        uriPassphrase: "123456",
    },
    {
        name: "non-ascii passphrase and password",
        couchDB_URI: "http://couch.local:5984",
        couchDB_USER: "사용자",
        couchDB_PASSWORD: "비밀번호🔐",
        couchDB_DBNAME: "vault",
        passphrase: "한국어 패스프레이즈 ✔️",
        uriPassphrase: "암호",
    },
];

// ---------------------------------------------------------------------------

function goRun(subcommand, input, { expectFailure = false } = {}) {
    const stdout = execFileSync("go", ["run", "./cmd/couchhub", subcommand], {
        cwd: repoRoot,
        input: JSON.stringify(input),
        encoding: "utf8",
        maxBuffer: 8 * 1024 * 1024,
        // Negative cases are supposed to exit non-zero; don't let their stderr
        // masquerade as a real problem in the log.
        stdio: expectFailure ? ["pipe", "pipe", "ignore"] : ["pipe", "pipe", "inherit"],
    });
    return JSON.parse(stdout);
}

// Rebuilds what utils/setup/generate_setup_uri.ts would produce, so we can
// compare field-by-field rather than trusting our reading of it.
function officialSettings(c) {
    const settings = createNewVaultSettings();
    Object.assign(settings, PREFERRED_SETTING_SELF_HOSTED, {
        couchDB_URI: c.couchDB_URI,
        couchDB_USER: c.couchDB_USER,
        couchDB_PASSWORD: c.couchDB_PASSWORD,
        couchDB_DBNAME: c.couchDB_DBNAME,
        batchSave: true,
        periodicReplication: true,
        syncOnStart: true,
        syncOnFileOpen: true,
        syncAfterMerge: true,
    });
    Object.assign(settings, {
        isConfigured: true,
        encrypt: true,
        passphrase: c.passphrase,
        usePathObfuscation: true,
    });
    upsertRemoteConfigurationInPlace(settings, "couchdb", { activate: true });
    return settings;
}

// The pruning encodeSettingsToSetupURI applies with skipDefaultValue = true.
function prune(input) {
    const setting = { ...input };
    for (const k of Object.keys(setting)) {
        const lhs = JSON.stringify(k in setting ? setting[k] : "");
        const rhs = JSON.stringify(k in DEFAULT_SETTINGS ? DEFAULT_SETTINGS[k] : "*");
        if (lhs === rhs) delete setting[k];
    }
    for (const prop of ["pluginSyncExtendedSetting", "doNotUseFixedRevisionForChunks"]) {
        delete setting[prop];
    }
    for (const prop of ["configPassphraseStore", "encryptedCouchDBConnection", "encryptedPassphrase"]) {
        setting[prop] = "";
    }
    return setting;
}

const failures = [];

function check(caseName, label, condition, detail = "") {
    if (condition) return true;
    failures.push(`${caseName} / ${label}${detail ? `: ${detail}` : ""}`);
    return false;
}

function diffStable(caseName, label, got, want) {
    const keys = new Set([...Object.keys(got), ...Object.keys(want)].filter((k) => !VOLATILE.has(k)));
    const problems = [];
    for (const k of [...keys].sort()) {
        const g = JSON.stringify(got[k]);
        const w = JSON.stringify(want[k]);
        if (g !== w) problems.push(`${k}: got ${g}, want ${w}`);
    }
    check(caseName, label, problems.length === 0, problems.join("; "));
}

// The one remote configuration must match apart from its random id.
function diffRemoteConfig(caseName, got, want) {
    const gEntries = Object.entries(got.remoteConfigurations ?? {});
    const wEntries = Object.entries(want.remoteConfigurations ?? {});

    if (!check(caseName, "remote config count", gEntries.length === 1 && wEntries.length === 1,
        `got ${gEntries.length}, want ${wEntries.length}`)) {
        return;
    }
    const [gId, gCfg] = gEntries[0];
    const [, wCfg] = wEntries[0];

    check(caseName, "activeConfigurationId points at the config", got.activeConfigurationId === gId,
        `activeConfigurationId=${got.activeConfigurationId} id=${gId}`);
    check(caseName, "remote config id shape", /^remote-[0-9a-z]+-[0-9a-z]{6}$/.test(gId), gId);
    check(caseName, "remote config name", gCfg.name === wCfg.name, `got ${gCfg.name}, want ${wCfg.name}`);
    check(caseName, "remote config uri", gCfg.uri === wCfg.uri, `\n      got  ${gCfg.uri}\n      want ${wCfg.uri}`);
    check(caseName, "remote config isEncrypted", gCfg.isEncrypted === wCfg.isEncrypted);
    check(caseName, "remote config id field matches key", gCfg.id === gId);
}

console.log(`cross-checking internal/setupuri against @vrtmrz/livesync-commonlib\n`);

for (const c of CASES) {
    const before = failures.length;

    // --- forward: Go writes, the official library reads -------------------
    const go = goRun("gen-setup-uri", c);
    const decoded = await decodeSettingsFromSetupURI(go.uri, c.uriPassphrase);

    if (!check(c.name, "official library decodes our URI", decoded !== false)) continue;

    diffStable(c.name, "decoded == our own settings", decoded, go.settings);

    // --- shape: our settings vs the official generator's -------------------
    const want = prune(officialSettings(c));
    diffStable(c.name, "settings match official generator", go.settings, want);
    diffRemoteConfig(c.name, go.settings, want);

    // --- reverse: the official library writes, Go reads --------------------
    const officialURI = (
        await encodeSettingsToSetupURI(
            officialSettings(c),
            c.uriPassphrase,
            ["pluginSyncExtendedSetting", "doNotUseFixedRevisionForChunks"],
            true
        )
    ).trim();
    const roundTripped = goRun("parse-setup-uri", { uri: officialURI, uriPassphrase: c.uriPassphrase });
    diffStable(c.name, "we decode the official URI", roundTripped, want);
    diffRemoteConfig(c.name, roundTripped, want);

    // --- the unencrypted `?settingsQR=` payload ----------------------------
    // Byte-for-byte against the reference encoder: this format is a positional
    // array, so a single wrong slot would put a password into another field
    // without any error.
    const QR_BASE = "obsidian://setuplivesync?settingsQR="
    check(c.name, "qr uri prefix", go.qrUri.startsWith(QR_BASE), go.qrUri.slice(0, 40))

    const goQrPayload = decodeURIComponent(go.qrUri.slice(QR_BASE.length))
    const wantQrPayload = encodeSettingsToQRCodeData(go.settings)
    check(c.name, "qr payload matches the reference encoder", goQrPayload === wantQrPayload,
        goQrPayload === wantQrPayload ? "" : `\n      got  ${goQrPayload.slice(0, 160)}\n      want ${wantQrPayload.slice(0, 160)}`)

    // ...and it must decode back to the same credentials, since that is what the
    // plugin actually does with it.
    const fromQr = decodeSettingsFromQRCodeData(goQrPayload)
    for (const key of ["couchDB_URI", "couchDB_USER", "couchDB_PASSWORD", "couchDB_DBNAME", "passphrase"]) {
        check(c.name, `qr round trip ${key}`, fromQr[key] === go.settings[key],
            `got ${JSON.stringify(fromQr[key])}, want ${JSON.stringify(go.settings[key])}`)
    }
    check(c.name, "qr round trip encrypt", fromQr.encrypt === true)

    // --- wrong passphrase must fail, not return garbage --------------------
    let rejected = false;
    try {
        goRun(
            "parse-setup-uri",
            { uri: go.uri, uriPassphrase: c.uriPassphrase + "x" },
            { expectFailure: true }
        );
    } catch {
        rejected = true;
    }
    check(c.name, "wrong passphrase is rejected", rejected);

    const ok = failures.length === before;
    console.log(`  ${ok ? "PASS" : "FAIL"}  ${c.name}${ok ? `  (uri ${go.uri.length} bytes)` : ""}`);
}

if (failures.length > 0) {
    console.error(`\n${failures.length} failure(s):`);
    for (const f of failures) console.error(`  - ${f}`);
    process.exit(1);
}

console.log(`\nall ${CASES.length} cases passed`);
