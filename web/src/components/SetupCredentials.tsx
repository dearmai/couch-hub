import { AlertTriangle, ExternalLink, Monitor, Smartphone } from "lucide-react"

import { CopyField } from "@/components/CopyField"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { buttonVariants } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import type { Credentials } from "@/lib/vaults"

/**
 * Everything needed to set up a client.
 *
 * The QR is livesync's `?settingsQR=` form: the settings, including the E2EE
 * passphrase, travel in the code itself. That is what makes it a single scan
 * with nothing to type - and it is also why anyone who sees the code gets the
 * vault. The PIN-protected `?settings=` form is kept below for handing a vault
 * over a channel where that is not acceptable.
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

      {credentials.plainQrSvg ? (
        <div className="flex flex-col items-center gap-3">
          {/* The SVG comes from our own server, generated from the URI we just
              requested - not from user input.

              [&_svg] overrides the intrinsic width/height the generator emits:
              a dense QR is nearly 600px square and would otherwise push the
              dialog into horizontal scrolling on a phone. */}
          <div
            className="w-full max-w-[280px] rounded-lg border bg-white p-3 [&_svg]:block [&_svg]:h-auto [&_svg]:w-full"
            dangerouslySetInnerHTML={{ __html: credentials.plainQrSvg }}
          />
          <p className="text-muted-foreground flex items-center gap-1.5 text-center text-xs">
            <Smartphone className="size-3.5 shrink-0" aria-hidden />
            휴대폰 카메라로 스캔하면 Obsidian이 열리고 동기화 설정이 바로 적용됩니다
          </p>
          {/* Same protocol handler the camera triggers, for when Obsidian runs on
              this machine and there is no camera involved. */}
          {/* An anchor rather than a Button: this shadcn Button has no asChild
              escape hatch, and the browser must follow the obsidian:// scheme
              itself for the OS to hand it to Obsidian. */}
          <a href={credentials.plainSetupUri} className={buttonVariants({ variant: "outline", size: "sm" })}>
            <ExternalLink aria-hidden /> 이 컴퓨터의 Obsidian에서 열기
          </a>
        </div>
      ) : (
        <Alert>
          <AlertTriangle aria-hidden />
          <AlertTitle>QR 코드를 만들 수 없습니다</AlertTitle>
          <AlertDescription>
            Setup URI가 QR 용량을 넘었습니다. 아래 URI를 복사해서 옮기세요.
            {credentials.plainQrError ? (
              <span className="mt-1 block font-mono text-xs">{credentials.plainQrError}</span>
            ) : null}
          </AlertDescription>
        </Alert>
      )}

      <Alert variant="destructive">
        <AlertTriangle aria-hidden />
        <AlertTitle>이 QR에는 자격증명이 그대로 들어 있습니다</AlertTitle>
        <AlertDescription>
          입력 단계가 없는 이유는 CouchDB 비밀번호와 E2EE 패스프레이즈를 QR이 평문으로 담고 있기 때문입니다. 스캔하거나
          사진을 찍은 사람은 누구나 Vault 전체를 열 수 있습니다. 화면에 띄워둔 채 자리를 뜨거나 캡처를 남기지 마세요.
        </AlertDescription>
      </Alert>

      <Alert>
        <AlertTriangle aria-hidden />
        <AlertTitle>이 URI는 Obsidian의 &quot;Enter Setup URI&quot; 창에 붙여넣을 수 없습니다</AlertTitle>
        <AlertDescription>
          그 창은 암호화된 URI만 받습니다. 카메라로 스캔하거나 위 버튼으로 여세요. 붙여넣기로 설정하려면 아래 &quot;컴퓨터에서
          붙여넣기로 설정&quot;의 URI와 PIN을 쓰세요.
        </AlertDescription>
      </Alert>

      <div className="space-y-3">
        <CopyField label="Setup URI" value={credentials.plainSetupUri} secret />
        <CopyField label="E2EE 패스프레이즈" value={credentials.e2eePassphrase} secret />
        <div className="grid gap-3 sm:grid-cols-2">
          <CopyField label="CouchDB 계정" value={credentials.couchUser} />
          <CopyField label="CouchDB 비밀번호" value={credentials.couchPassword} secret />
        </div>
      </div>

      <Separator />

      {/* Kept visible rather than behind a disclosure: on a desktop there is no
          camera, so this is the only path - hiding it made the primary flow
          look like the only one. */}
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
            <span className="text-foreground font-medium">패스프레이즈 칸에 아래 PIN 6자리</span> 입력
          </li>
        </ol>

        <div className="text-center">
          <div className="text-muted-foreground text-xs font-medium">패스프레이즈로 쓸 PIN</div>
          <div className="font-mono text-2xl tracking-[0.3em] tabular-nums" aria-label="Setup PIN">
            {credentials.setupPin}
          </div>
        </div>
        <CopyField label="Setup URI (PIN 보호)" value={credentials.setupUri} secret />
      </section>

      <Alert>
        <AlertTriangle aria-hidden />
        <AlertTitle>E2EE 패스프레이즈를 따로 보관하세요</AlertTitle>
        <AlertDescription>
          이 값이 Vault 내용을 복호화합니다. 잃어버리면 서버에 저장된 노트를 복구할 수 없고, 유출되면 서버 비밀번호를
          바꿔도 이미 저장된 데이터는 읽힙니다.
        </AlertDescription>
      </Alert>
    </div>
  )
}
