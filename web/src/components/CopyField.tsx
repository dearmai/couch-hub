import { useState } from "react"
import { Check, Copy, Eye, EyeOff } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/**
 * A read-only credential with a copy button, and optional masking.
 *
 * Masking matters because these panels get screen-shared and photographed:
 * the passphrase is what decrypts the vault, so it stays hidden until asked for.
 */
export function CopyField({
  label,
  value,
  secret = false,
  className,
}: {
  label: string
  value: string
  secret?: boolean
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  const [revealed, setRevealed] = useState(!secret)

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // Clipboard access is blocked outside secure contexts, which is the norm
      // for a homelab panel reached over plain http on a LAN address.
      // Fall back to selecting the text so the user can copy it manually.
      const el = document.getElementById(`copy-${label}`) as HTMLInputElement | null
      el?.select()
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className={cn("space-y-1.5", className)}>
      <div className="text-muted-foreground text-xs font-medium">{label}</div>
      <div className="flex items-center gap-1.5">
        <input
          id={`copy-${label}`}
          aria-label={label}
          readOnly
          value={revealed ? value : "•".repeat(Math.min(value.length, 32))}
          className="bg-muted min-w-0 flex-1 truncate rounded-md px-2.5 py-2 font-mono text-xs"
        />
        {secret ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            // Deliberately not "...보기": the vault detail page has its own
            // "Setup URI 보기" button, and two controls with the same
            // accessible name are ambiguous to both screen readers and tests.
            aria-label={revealed ? `${label} 숨기기` : `${label} 표시`}
            onClick={() => setRevealed((v) => !v)}
          >
            {revealed ? <EyeOff aria-hidden /> : <Eye aria-hidden />}
          </Button>
        ) : null}
        <Button type="button" variant="ghost" size="icon" aria-label={`${label} 복사`} onClick={copy}>
          {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        </Button>
      </div>
    </div>
  )
}
