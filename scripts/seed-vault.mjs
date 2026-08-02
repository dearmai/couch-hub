// Seeds a CouchDB database with notes in obsidian-livesync's own format, using
// the official encryption, so the document reader can be tested against data it
// did not produce.
//
//   node scripts/seed-vault.mjs '{"couchdb":"...","db":"...","user":"...","password":"...","passphrase":"...","notes":[{"path":"a.md","text":"# hi"}]}'
//
// Writes one entry per note plus one chunk per note, matching the shape the
// plugin writes: an entry lists chunk ids, and both its path and every chunk are
// ciphertext when a passphrase is given.

import { encrypt as encryptHKDF, encryptWithEphemeralSalt, createPBKDF2Salt } from "octagonal-wheels/encryption/hkdf"

const input = JSON.parse(process.argv[2] ?? "{}")
const {
    couchdb = "http://127.0.0.1:15984",
    db,
    user = "admin",
    password = "couchhub-dev",
    passphrase = "",
    notes = [],
    // The current plugin wraps path, timestamps, size and the chunk list into a
    // single encrypted bundle on the `path` field. Seed that shape by default;
    // set false to seed the older plain-encrypted-path form.
    encryptedMeta = true,
} = input

const ENCRYPTED_META_PREFIX = "/\\:"
const DOCID_SYNC_PARAMETERS = "_local/obsidian_livesync_sync_parameters"

if (!db) {
    console.error("db is required")
    process.exit(1)
}

const auth = "Basic " + Buffer.from(`${user}:${password}`).toString("base64")

async function put(path, body) {
    const res = await fetch(`${couchdb}${path}`, {
        method: "PUT",
        headers: { Authorization: auth, "Content-Type": "application/json" },
        body: JSON.stringify(body),
    })
    if (!res.ok && res.status !== 409) {
        throw new Error(`PUT ${path} -> ${res.status} ${await res.text()}`)
    }
    return res
}

/** Encrypts when a passphrase is set; a vault with encryption off stores plain text. */
async function maybeEncrypt(value) {
    if (!passphrase) return value
    return await encryptWithEphemeralSalt(value, passphrase)
}

/** livesync keys a note by its path; mirror that so repeated seeding is idempotent. */
function idFor(path) {
    return path.replace(/[^a-zA-Z0-9._/-]/g, "_")
}

// The %= format keys off a salt stored in the vault, so it has to exist before
// anything is written with it.
let pbkdf2Salt = null
if (passphrase && encryptedMeta) {
    pbkdf2Salt = createPBKDF2Salt()
    const saltB64 = Buffer.from(pbkdf2Salt).toString("base64")
    const existing = await fetch(`${couchdb}/${db}/${encodeURIComponent(DOCID_SYNC_PARAMETERS)}`, {
        headers: { Authorization: auth },
    })
    const rev = existing.ok ? (await existing.json())._rev : undefined
    await put(`/${db}/${encodeURIComponent(DOCID_SYNC_PARAMETERS)}`, {
        _id: DOCID_SYNC_PARAMETERS,
        ...(rev ? { _rev: rev } : {}),
        type: "sync-parameters",
        protocolVersion: 2,
        pbkdf2salt: saltB64,
    })
}

let seeded = 0
for (const note of notes) {
    const base = idFor(note.path)
    // Chunk ids in livesync are content-addressed with an "h:" prefix. The exact
    // hash does not matter here - only that the entry points at them.
    const chunkIds = []
    // Split into two chunks for at least one note, so reassembly order is
    // actually exercised rather than assumed.
    const parts = note.text.length > 40 ? [note.text.slice(0, 20), note.text.slice(20)] : [note.text]

    for (const [j, part] of parts.entries()) {
        const id = `h:${base}-${j}`
        chunkIds.push(id)
        await put(`/${db}/${encodeURIComponent(id)}`, {
            _id: id,
            type: "leaf",
            data: await maybeEncrypt(part),
        })
    }

    const docId = base
    const mtime = note.mtime ?? Date.now()

    if (passphrase && encryptedMeta) {
        // Everything the reader needs is inside the bundle; the outer document
        // deliberately carries no usable path, size or chunk list, exactly as the
        // plugin writes it.
        const meta = JSON.stringify({
            path: note.path,
            mtime,
            ctime: mtime,
            size: note.text.length,
            children: chunkIds,
        })
        await put(`/${db}/${encodeURIComponent(docId)}`, {
            _id: docId,
            path: ENCRYPTED_META_PREFIX + (await encryptHKDF(meta, passphrase, pbkdf2Salt)),
            type: "plain",
            children: [],
            mtime: 0,
            ctime: 0,
            size: 0,
        })
    } else {
        await put(`/${db}/${encodeURIComponent(docId)}`, {
            _id: docId,
            path: await maybeEncrypt(note.path),
            type: "plain",
            children: chunkIds,
            mtime,
            ctime: mtime,
            size: note.text.length,
        })
    }
    seeded++
}

console.log(JSON.stringify({ seeded }))
