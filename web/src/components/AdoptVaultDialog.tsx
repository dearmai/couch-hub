import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Loader2 } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { ApiError } from "@/lib/api"
import { formatBytes, formatCount } from "@/lib/stats"
import { adoptVaultSchema, vaultsApi, type AdoptVaultValues, type VaultWithCredentials } from "@/lib/vaults"

/**
 * Takes an existing CouchDB database under management.
 *
 * The passphrase is asked for rather than generated: it is what decrypts what
 * is already stored, so a fresh one would produce a client that connects and
 * then cannot read a single note.
 */
export function AdoptVaultDialog({
  open,
  onOpenChange,
  onAdopted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onAdopted: (result: VaultWithCredentials) => void
}) {
  const queryClient = useQueryClient()

  const { data: databases, isPending: loadingDatabases } = useQuery({
    queryKey: ["couch", "databases"],
    queryFn: vaultsApi.databases,
    enabled: open,
  })

  const form = useForm<AdoptVaultValues>({
    resolver: zodResolver(adoptVaultSchema),
    defaultValues: { dbName: "", name: "", e2eePassphrase: "", e2eeDisabled: false },
  })

  const adopt = useMutation({
    mutationFn: vaultsApi.adopt,
    onSuccess: async (data) => {
      onOpenChange(false)
      form.reset()
      onAdopted(data)
      await queryClient.invalidateQueries({ queryKey: ["vaults"] })
      await queryClient.invalidateQueries({ queryKey: ["couch", "databases"] })
      await queryClient.invalidateQueries({ queryKey: ["status"] })
    },
  })

  const available = (databases ?? []).filter((d) => !d.registered)
  const e2eeDisabled = form.watch("e2eeDisabled")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>기존 데이터베이스 추가</DialogTitle>
          <DialogDescription>
            이미 있는 CouchDB 데이터베이스를 CouchHub 관리 아래로 가져옵니다. 데이터베이스는 새로 만들지도, 비우지도
            않습니다.
          </DialogDescription>
        </DialogHeader>

        <form id="adopt-vault" onSubmit={form.handleSubmit((v) => adopt.mutate(v))}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="adopt-db">데이터베이스</FieldLabel>
              <Select
                value={form.watch("dbName")}
                // base-ui's Select yields null when cleared; the field is a string.
                onValueChange={(v) => form.setValue("dbName", v ?? "")}
              >
                <SelectTrigger id="adopt-db">
                  <SelectValue placeholder={loadingDatabases ? "불러오는 중…" : "선택하세요"} />
                </SelectTrigger>
                <SelectContent>
                  {available.map((d) => (
                    <SelectItem key={d.name} value={d.name}>
                      {d.name} · 문서 {formatCount(d.docCount)} · {formatBytes(d.sizeFile)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>
                {loadingDatabases
                  ? "서버의 데이터베이스를 조회하는 중입니다."
                  : available.length === 0
                    ? "추가할 수 있는 데이터베이스가 없습니다. 시스템 데이터베이스와 이미 등록된 것은 제외됩니다."
                    : "시스템 데이터베이스와 이미 등록된 것은 목록에서 제외됩니다."}
              </FieldDescription>
              <FieldError errors={[form.formState.errors.dbName].filter(Boolean)} />
            </Field>

            <Field>
              <FieldLabel htmlFor="adopt-name">표시 이름 (선택)</FieldLabel>
              <Input id="adopt-name" autoComplete="off" placeholder="비우면 DB 이름 사용" {...form.register("name")} />
            </Field>

            <Field orientation="horizontal">
              <FieldContentRow
                label="암호화를 쓰지 않는 Vault"
                description="livesync에서 End-to-End Encryption을 꺼둔 채 쓰던 경우에만 켜세요."
              />
              <Switch
                checked={e2eeDisabled}
                onCheckedChange={(checked) => {
                  form.setValue("e2eeDisabled", checked)
                  if (checked) form.setValue("e2eePassphrase", "")
                }}
                aria-label="암호화를 쓰지 않는 Vault"
              />
            </Field>

            {!e2eeDisabled ? (
              <Field>
                <FieldLabel htmlFor="adopt-passphrase">기존 E2EE 패스프레이즈</FieldLabel>
                <Input
                  id="adopt-passphrase"
                  type="password"
                  autoComplete="off"
                  {...form.register("e2eePassphrase")}
                />
                <FieldDescription>
                  Obsidian에서 쓰던 값과 정확히 같아야 합니다. 틀리면 연결은 되지만 이미 저장된 노트를 하나도 읽지
                  못합니다.
                </FieldDescription>
                <FieldError errors={[form.formState.errors.e2eePassphrase].filter(Boolean)} />
              </Field>
            ) : null}
          </FieldGroup>

          <Alert className="mt-4">
            <AlertTriangle aria-hidden />
            <AlertTitle>이 Vault에 접근할 전용 계정이 추가됩니다</AlertTitle>
            <AlertDescription>
              기존 권한 설정은 유지되고 계정만 추가됩니다. 나중에 제거할 때 데이터를 남길지 선택할 수 있습니다.
            </AlertDescription>
          </Alert>

          {adopt.isError ? (
            <Alert variant="destructive" className="mt-4">
              <AlertTriangle aria-hidden />
              <AlertTitle>추가 실패</AlertTitle>
              <AlertDescription>
                {adopt.error instanceof ApiError ? adopt.error.message : String(adopt.error)}
              </AlertDescription>
            </Alert>
          ) : null}
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={adopt.isPending}>
            취소
          </Button>
          <Button type="submit" form="adopt-vault" disabled={adopt.isPending}>
            {adopt.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            추가
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function FieldContentRow({ label, description }: { label: string; description: string }) {
  return (
    <div className="space-y-0.5">
      <div className="text-sm font-medium">{label}</div>
      <div className="text-muted-foreground text-xs">{description}</div>
    </div>
  )
}
