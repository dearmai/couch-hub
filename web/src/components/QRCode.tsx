import { useState } from "react"
import { Maximize2 } from "lucide-react"

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"

/**
 * Renders a QR at a size a phone camera can actually resolve.
 *
 * The code is sized from its module count rather than to a fixed width. A Setup
 * URI is around 1.4 KB, which is 129 modules a side - at the 280px a dialog
 * used to give it, that is under two device pixels per module, and cameras give
 * up well before the code stops fitting on the screen.
 *
 * Clicking it fills the window, which is the honest answer for a dense code:
 * three or four times the module size beats any amount of coaxing at thumbnail
 * scale.
 */
export function QRCode({
  svg,
  modules,
  label,
  hint,
  maxWidth = 420,
}: {
  svg: string
  modules: number
  label: string
  /** Shown beside the enlarged code - the PIN, so scanning and typing it do not need two screens. */
  hint?: string
  maxWidth?: number
}) {
  const [zoomed, setZoomed] = useState(false)

  // Three CSS pixels a module, floored at a size that stays usable when the
  // module count is unknown - an older issue, or a URI short enough not to need
  // the room.
  const width = Math.min(maxWidth, Math.max(280, (modules || 129) * 3))

  return (
    <>
      <button
        type="button"
        onClick={() => setZoomed(true)}
        className="group relative w-full rounded-lg border bg-white p-3 transition-shadow hover:shadow-md [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
        style={{ maxWidth: `${width}px` }}
        aria-label={`${label} 크게 보기`}
        // The SVG comes from our own server, generated from the URI just
        // requested - not from user input.
        dangerouslySetInnerHTML={{ __html: svg }}
      />
      <button
        type="button"
        onClick={() => setZoomed(true)}
        className="text-muted-foreground hover:text-foreground flex items-center gap-1.5 text-xs"
      >
        <Maximize2 className="size-3.5" aria-hidden /> 스캔이 안 되면 크게 보기
      </button>

      <Dialog open={zoomed} onOpenChange={setZoomed}>
        <DialogContent className="w-auto max-w-[calc(100%-2rem)] sm:max-w-[min(92vw,86vh)]">
          <DialogHeader>
            <DialogTitle>{label}</DialogTitle>
            <DialogDescription>휴대폰 카메라를 화면에 대세요. 어두운 방에서는 화면 밝기를 올리면 잘 읽힙니다.</DialogDescription>
          </DialogHeader>
          <div
            className="mx-auto w-full rounded-lg bg-white p-4 [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
            style={{ width: "min(88vw, 70vh)" }}
            dangerouslySetInnerHTML={{ __html: svg }}
          />

          {hint ? (
            <div className="text-center">
              <div className="text-muted-foreground text-xs font-medium">Obsidian이 물어보면 입력할 PIN</div>
              <div className="font-mono text-3xl tracking-[0.3em] tabular-nums">{hint}</div>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}
