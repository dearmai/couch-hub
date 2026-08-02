// Dev-only. Regenerates internal/setupuri/template.json from the official
// obsidian-livesync library, so the Go side never has to reimplement
// createNewVaultSettings()/upsertRemoteConfigurationInPlace()/DEFAULT_SETTINGS.
//
// Mirrors utils/setup/generate_setup_uri.ts (createCouchDBSettings) and the
// pruning half of encodeSettingsToSetupURI, stopping just before encryption.
//
//   node scripts/gen-template.mjs
//
// Placeholders below are substituted by internal/setupuri at runtime.

import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { createNewVaultSettings } from "@vrtmrz/livesync-commonlib/settings";
import {
    DEFAULT_SETTINGS,
    KeyIndexOfSettings,
    PREFERRED_SETTING_SELF_HOSTED,
} from "@vrtmrz/livesync-commonlib/compat/common/types";

// Same defaults encodeSettingsToSetupURI() is called with by the official generator.
const REMOVE_PROPERTIES = ["pluginSyncExtendedSetting", "doNotUseFixedRevisionForChunks"];
const NECESSARY_ERASURE_PROPERTIES = [
    "configPassphraseStore",
    "encryptedCouchDBConnection",
    "encryptedPassphrase",
];

// Sentinels the Go side replaces. Chosen so they can never collide with a real value.
const PLACEHOLDER = {
    uri: "@@COUCHHUB_COUCHDB_URI@@",
    user: "@@COUCHHUB_COUCHDB_USER@@",
    password: "@@COUCHHUB_COUCHDB_PASSWORD@@",
    dbname: "@@COUCHHUB_COUCHDB_DBNAME@@",
    passphrase: "@@COUCHHUB_E2EE_PASSPHRASE@@",
};

function buildCouchDBSettings() {
    const settings = createNewVaultSettings();
    Object.assign(settings, PREFERRED_SETTING_SELF_HOSTED, {
        couchDB_URI: PLACEHOLDER.uri,
        couchDB_USER: PLACEHOLDER.user,
        couchDB_PASSWORD: PLACEHOLDER.password,
        couchDB_DBNAME: PLACEHOLDER.dbname,
        batchSave: true,
        periodicReplication: true,
        syncOnStart: true,
        syncOnFileOpen: true,
        syncAfterMerge: true,
    });
    // applyEncryptedVaultSettings
    Object.assign(settings, {
        isConfigured: true,
        encrypt: true,
        passphrase: PLACEHOLDER.passphrase,
        usePathObfuscation: true,
    });
    // NOTE: upsertRemoteConfigurationInPlace() is deliberately NOT called here.
    // It derives remoteConfigurations[id] (random id + a WHATWG-URL-encoded
    // connection string) from the live credentials, so it cannot be baked into a
    // static template. internal/setupuri/connstring.go reproduces it at runtime and
    // scripts/verify-setup-uri.mjs proves the reproduction matches this library.
    return settings;
}

// The pruning half of encodeSettingsToSetupURI(settings, _, REMOVE_PROPERTIES, true).
function prune(input) {
    const setting = { ...input };

    // skipDefaultValue === true
    for (const k of Object.keys(setting)) {
        const lhs = JSON.stringify(k in setting ? setting[k] : "");
        const rhs = JSON.stringify(k in DEFAULT_SETTINGS ? DEFAULT_SETTINGS[k] : "*");
        if (lhs === rhs) delete setting[k];
    }
    for (const prop of REMOVE_PROPERTIES) delete setting[prop];
    for (const prop of NECESSARY_ERASURE_PROPERTIES) setting[prop] = "";

    return setting;
}

const settings = prune(buildCouchDBSettings());

// Guard: every placeholder must survive pruning. If the plugin ever makes one of
// these a default value it would be silently dropped and the URI would be broken.
const serialised = JSON.stringify(settings);
for (const [name, token] of Object.entries(PLACEHOLDER)) {
    if (!serialised.includes(token)) {
        throw new Error(
            `placeholder "${name}" (${token}) was pruned away — ` +
                `the livesync defaults changed and internal/setupuri needs review`
        );
    }
}

const here = dirname(fileURLToPath(import.meta.url));
const outDir = resolve(here, "../internal/setupuri");
mkdirSync(outDir, { recursive: true });

const templatePath = resolve(outDir, "template.json");
writeFileSync(templatePath, JSON.stringify(settings, null, 2) + "\n");
console.log(`wrote ${templatePath}`);
console.log(`  ${Object.keys(settings).length} keys, ${serialised.length} bytes serialised`);

// The `?settingsQR=` payload is a positional array: each setting is written at
// its index in KeyIndexOfSettings. The mapping is generated rather than
// hand-copied because it shifts whenever the plugin gains a setting, and a
// stale copy would silently write values into the wrong fields.
// Insertion order is preserved deliberately. Two keys currently share a slot,
// and the reference encoder assigns every key in Object.entries order -
// including absent ones, as undefined - so the later key wins. Sorting here
// would change which value survives.
const qrOrder = Object.entries(KeyIndexOfSettings).filter(([, index]) => index >= 0)
const maxIndex = Math.max(...qrOrder.map(([, index]) => index));

// Upstream currently maps two keys to the same slot. Object.entries order
// decides which one wins, so record it rather than letting it surprise us later.
const bySlot = new Map();
for (const [key, index] of Object.entries(KeyIndexOfSettings)) {
    if (index < 0) continue;
    bySlot.set(index, [...(bySlot.get(index) ?? []), key]);
}
const collisions = [...bySlot.entries()].filter(([, keys]) => keys.length > 1);

const qrIndexPath = resolve(outDir, "qrindex.json");
writeFileSync(
    qrIndexPath,
    JSON.stringify({ maxIndex, order: qrOrder.map(([key, index]) => ({ key, index })) }, null, 2) + "\n"
);
console.log(`wrote ${qrIndexPath}`);
console.log(`  ${qrOrder.length} keys, max index ${maxIndex}`);
for (const [slot, keys] of collisions) {
    console.log(`  note: slot ${slot} is shared by ${keys.join(", ")} (upstream)`);
}
