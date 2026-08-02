import { AlertTriangle, ExternalLink, Monitor, ShieldAlert, Smartphone } from "lucide-react"

import { CopyField } from "@/components/CopyField"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { buttonVariants } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import type { Credentials } from "@/lib/vaults"

/**
 * Everything needed to set up a client.
 *
 * The code on offer is livesync's `?settings=` form: encrypted under a PIN that
 * this vault will not accept for long. Scanning it opens Obsidian, which asks
 * for the PIN - so a photographed screen is worth nothing without the six
 * digits, and worth nothing at all once they have been replaced.
 *
 * The `?settingsQR=` form carries the credentials in the clear and needs no PIN.
 * It is one click behind a disclosure rather than gone: it is the only way to
 * configure a client that cannot be handed a passphrase.
 *
 * The plugin has no in-app camera; its "Scan QR Code" dialog only instructs the
 * user to scan with the phone's camera app, which hands the obsidian:// URL to
 * Obsidian.
 */
export function SetupCredentials({
  credentials,
  persisted,
}: {
  credentials: Credentials
  persisted: boolean
}) {
  return (
    <div className="space-y-5">
      {!persisted ? (
        <Alert variant="destructive">
          <AlertTriangle aria-hidden />
          <AlertTitle>지금만 볼 수 있습니다</AlertTitle>
          <AlertDescription>
            COUCHHUB_SECRET이 설정되지 않아 자격증명이 저장되지 않았습니다. 이 화면을 벗어나면 다시 발급할 수 없고,
            Vault를 새로 만들어야 합니다.
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-5 md:grid-cols-[minmax(0,280px)_1fr] md:items-start">
        {credentials.qrSvg ? (
          <div className="flex flex-col items-center gap-3">
            {/* The SVG comes from our own server, generated from the URI we just
                requested - not from user input.

                [&_svg] overrides the intrinsic width/height the generator emits:
                a dense QR is nearly 600px square and would otherwise push the
                page into horizontal scrolling on a phone. */}
            <div
              className="w-full max-w-[280px] rounded-lg border bg-white p-3 [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
              dangerouslySetInnerHTML={{ __html: credentials.qrSvg }}
            />
            {/* Same protocol handler the camera triggers, for when Obsidian runs
                on this machine and there is no camera involved. An anchor rather
                than a Button: this shadcn Button has no asChild escape hatch,
                and the browser must follow the obsidian:// scheme itself for the
                OS to hand it to Obsidian. */}
            <a href={credentials.setupUri} className={buttonVariants({ variant: "outline", size: "sm" })}>
              <ExternalLink aria-hidden /> 이 컴퓨터의 Obsidian에서 열기
            </a>
          </div>
        ) : (
          <Alert>
            <AlertTriangle aria-hidden />
            <AlertTitle>QR 코드를 만들 수 없습니다</AlertTitle>
            <AlertDescription>
              Setup URI가 QR 용량을 넘었습니다. 아래 URI를 복사해서 옮기세요.
              {credentials.qrError ? <span className="mt-1 block font-mono text-xs">{credentials.qrError}</span> : null}
            </AlertDescription>
          </Alert>
        )}

        <div className="space-y-4">
          <div className="rounded-lg border p-4 text-center">
            <div className="text-muted-foreground text-xs font-medium">Obsidian이 물어보면 입력할 PIN</div>
            <div className="font-mono text-4xl tracking-[0.3em] tabular-nums" aria-label="Setup PIN">
              {credentials.setupPin}
            </div>
          </div>

          <ol className="text-muted-foreground list-decimal space-y-1.5 pl-5 text-sm">
            <li className="flex-1">
              <Smartphone className="mr-1 inline size-3.5" aria-hidden />
              휴대폰 카메라로 QR을 스캔하면 Obsidian이 열립니다
            </li>
            <li>패스프레이즈를 물으면 위 PIN 6자리를 입력합니다</li>
          </ol>

          <Separator />

          {/* Kept visible rather than behind a disclosure: on a desktop there is
              no camera, so this is the only path - hiding it made the primary
              flow look like the only one. */}
          <section className="space-y-3">
            <h3 className="flex items-center gap-1.5 text-sm font-medium">
              <Monitor className="size-4" aria-hidden /> 컴퓨터에서 붙여넣기로 설정
            </h3>
            <ol className="text-muted-foreground list-decimal space-y-1 pl-5 text-xs">
              <li>
                Obsidian 명령 팔레트에서 <span className="text-foreground font-medium">Use the setup URI</span> 실행
              </li>
              <li>아래 URI를 Setup-URI 칸에 붙여넣기</li>
              <li>
                <span className="text-foreground font-medium">패스프레이즈 칸에 위 PIN 6자리</span> 입력
              </li>
            </ol>
            <CopyField label="Setup URI (PIN 보호)" value={credentials.setupUri} secret />
          </section>
        </div>
      </div>

      <details className="rounded-lg border p-4 [&[open]>summary]:mb-3">
        <summary className="flex cursor-pointer items-center gap-1.5 text-sm font-medium">
          <ShieldAlert className="size-4" aria-hidden /> PIN 없이 연결하기 · 자격증명 직접 보기
        </summary>

        <div className="space-y-4">
          <Alert variant="destructive">
            <AlertTriangle aria-hidden />
            <AlertTitle>아래 QR에는 자격증명이 그대로 들어 있습니다</AlertTitle>
            <AlertDescription>
              PIN을 묻지 않는 이유는 CouchDB 비밀번호와 E2EE 패스프레이즈를 QR이 평문으로 담고 있기 때문입니다. 스캔하거나
              사진을 찍은 사람은 누구나 Vault 전체를 열 수 있고, 위 PIN을 새로 발급해도 이 코드는 계속 동작합니다.
            </AlertDescription>
          </Alert>

          {credentials.plainQrSvg ? (
            <div className="flex flex-col items-center gap-3">
              <div
                className="w-full max-w-[240px] rounded-lg border bg-white p-3 [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
                dangerouslySetInnerHTML={{ __html: credentials.plainQrSvg }}
              />
              <a href={credentials.plainSetupUri} className={buttonVariants({ variant: "outline", size: "sm" })}>
                <ExternalLink aria-hidden /> PIN 없이 이 컴퓨터에서 열기
              </a>
              <p className="text-muted-foreground text-center text-xs">
                이 URI는 Obsidian의 &quot;Enter Setup URI&quot; 창에 붙여넣을 수 없습니다. 그 창은 암호화된 URI만 받습니다.
              </p>
            </div>
          ) : null}

          <div className="space-y-3">
            <CopyField label="Setup URI (평문)" value={credentials.plainSetupUri} secret />
            <CopyField label="E2EE 패스프레이즈" value={credentials.e2eePassphrase} secret />
            <div className="grid gap-3 sm:grid-cols-2">
              <CopyField label="CouchDB 계정" value={credentials.couchUser} />
              <CopyField label="CouchDB 비밀번호" value={credentials.couchPassword} secret />
            </div>
          </div>

          <Alert>
            <AlertTriangle aria-hidden />
            <AlertTitle>E2EE 패스프레이즈를 따로 보관하세요</AlertTitle>
            <AlertDescription>
              이 값이 Vault 내용을 복호화합니다. 잃어버리면 서버에 저장된 노트를 복구할 수 없고, 유출되면 서버 비밀번호를
              바꿔도 이미 저장된 데이터는 읽힙니다.
            </AlertDescription>
          </Alert>
        </div>
      </details>
    </div>
  )
}
