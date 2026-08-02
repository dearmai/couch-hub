// Cross-checks internal/livesync's decryption against the real octagonal-wheels
// implementation, the way verify-setup-uri.mjs does for the Setup URI.
//
// These are formats CouchHub does not own. Getting one subtly wrong does not
// fail loudly - it renders a note as garbage, or worse, renders the wrong note.
// So every format the reader claims to support is produced here by the actual
// library and handed to the Go binary to read back.
//
// Run with: make verify-livesync

import { execFileSync } from "node:child_process"
import { resolve, dirname } from "node:path"
import { fileURLToPath } from "node:url"

import { encrypt as encryptV1V2 } from "octagonal-wheels/encryption/encryption"
import { encryptV3 } from "octagonal-wheels/encryption/encryptionv3"
import {
    encrypt as encryptHKDF,
    encryptWithEphemeralSalt,
    createPBKDF2Salt,
} from "octagonal-wheels/encryption/hkdf"

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..")

function goDecrypt(input) {
    const stdout = execFileSync(resolve(repoRoot, "bin/couchhub"), ["decrypt-chunk"], {
        input: JSON.stringify(input),
        encoding: "utf8",
        maxBuffer: 16 * 1024 * 1024,
        stdio: ["pipe", "pipe", "pipe"],
    })
    return JSON.parse(stdout).plaintext
}

const PLAINTEXTS = [
    "# 제목\n\n본문 한 줄.",
    "plain ascii",
    "",
    // Multi-byte and astral characters: chunk boundaries land mid-character in
    // real vaults, so the codec has to be byte-exact.
    "한국어 ✔️ 𠮷野家 🔐",
    "x".repeat(200_000),
]

const PASSPHRASES = ["vault-passphrase", "짧음", "a".repeat(40)]

const failures = []

function check(label, condition, detail = "") {
    if (condition) return
    failures.push(`${label}${detail ? `: ${detail}` : ""}`)
}

function report(label, got, want) {
    const ok = got === want
    check(label, ok, ok ? "" : `got ${JSON.stringify(got).slice(0, 80)}, want ${JSON.stringify(want).slice(0, 80)}`)
    return ok
}

console.log("cross-checking internal/livesync against octagonal-wheels\n")

for (const passphrase of PASSPHRASES) {
    const pbkdf2Salt = createPBKDF2Salt()
    const pbkdf2SaltB64 = Buffer.from(pbkdf2Salt).toString("base64")

    for (const plaintext of PLAINTEXTS) {
        const short = plaintext.length > 24 ? `${plaintext.slice(0, 24)}…(${plaintext.length})` : plaintext
        const where = `pass=${JSON.stringify(passphrase.slice(0, 8))} text=${JSON.stringify(short)}`

        // %$ - the current format, salt carried inline.
        const ephemeral = await encryptWithEphemeralSalt(plaintext, passphrase)
        check(`${where} / %$ prefix`, ephemeral.startsWith("%$"))
        report(`${where} / %$`, goDecrypt({ blob: ephemeral, passphrase }), plaintext)

        // %= - salt stored in the vault's sync-parameters document.
        const hkdf = await encryptHKDF(plaintext, passphrase, pbkdf2Salt)
        check(`${where} / %= prefix`, hkdf.startsWith("%="))
        report(
            `${where} / %=`,
            goDecrypt({ blob: hkdf, passphrase, pbkdf2Salt: pbkdf2SaltB64 }),
            plaintext,
        )

        // %= without the salt must fail rather than return anything.
        let refusedWithoutSalt = false
        try {
            goDecrypt({ blob: hkdf, passphrase })
        } catch {
            refusedWithoutSalt = true
        }
        check(`${where} / %= without salt is refused`, refusedWithoutSalt)

        // %~ - V3.
        const v3 = await encryptV3(plaintext, passphrase)
        check(`${where} / %~ prefix`, v3.startsWith("%~"))
        report(`${where} / %~`, goDecrypt({ blob: v3, passphrase }), plaintext)

        // % - V2, in both iteration modes.
        for (const dynamicIterations of [false, true]) {
            const v2 = await encryptV1V2(plaintext, passphrase, dynamicIterations)
            check(`${where} / % prefix`, v2.startsWith("%") && !v2.startsWith("%~") && !v2.startsWith("%="))
            report(
                `${where} / % dynamic=${dynamicIterations}`,
                goDecrypt({ blob: v2, passphrase, dynamicIterations }),
                plaintext,
            )
        }

        // A wrong passphrase must fail, never yield plausible output.
        let rejected = false
        try {
            goDecrypt({ blob: ephemeral, passphrase: passphrase + "x" })
        } catch {
            rejected = true
        }
        check(`${where} / wrong passphrase is rejected`, rejected)
    }
    console.log(`  passphrase ${JSON.stringify(passphrase.slice(0, 12))}: ${PLAINTEXTS.length} payloads checked`)
}

if (failures.length > 0) {
    console.error(`\n${failures.length} failure(s):`)
    for (const f of failures.slice(0, 20)) console.error(`  - ${f}`)
    process.exit(1)
}

console.log(`\nall formats round-tripped`)
