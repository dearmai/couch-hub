import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, CheckCircle2, Loader2, Pencil, Plus, Server, Star, Stethoscope, Trash2 } from "lucide-react"

import { PageHeader } from "@/components/PageHeader"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
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
import { Skeleton } from "@/components/ui/skeleton"
import { ApiError } from "@/lib/api"
import {
  profileSchema,
  profilesApi,
  profilesQuery,
  type Profile,
  type ProfileResponse,
  type ProfileValues,
} from "@/lib/profiles"

const EMPTY: ProfileValues = {
  name: "",
  adminBaseUrl: "http://couchdb:5984",
  publicBaseUrl: "",
  adminUser: "admin",
  adminPassword: "",
}

export default function CouchDBs() {
  // A single dialog for both actions: adding and editing ask for exactly the
  // same fields, and only the password rule differs.
  const [editing, setEditing] = useState<Profile | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [result, setResult] = useState<ProfileResponse | null>(null)
  const [removing, setRemoving] = useState<Profile | null>(null)
  const queryClient = useQueryClient()

  const { data: profiles, isPending } = useQuery(profilesQuery)

  const form = useForm<ProfileValues>({ resolver: zodResolver(profileSchema), defaultValues: EMPTY })

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ["profiles"] })
    await queryClient.invalidateQueries({ queryKey: ["status"] })
    // The adopt dialog lists databases per server; a changed server changes it.
    await queryClient.invalidateQueries({ queryKey: ["couch", "databases"] })
  }

  const save = useMutation({
    mutationFn: (values: ProfileValues) =>
      editing ? profilesApi.update(editing.id, values) : profilesApi.create(values),
    onSuccess: async (data) => {
      setFormOpen(false)
      setResult(data)
      form.reset(EMPTY)
      await invalidate()
    },
  })

  const setPrimary = useMutation({
    mutationFn: profilesApi.setPrimary,
    onSuccess: invalidate,
  })

  const diagnose = useMutation({
    mutationFn: profilesApi.diagnose,
    onSuccess: async (data) => {
      setResult(data)
      await invalidate()
    },
  })

  const remove = useMutation({
    mutationFn: profilesApi.remove,
    onSuccess: async () => {
      setRemoving(null)
      await invalidate()
    },
  })

  function openCreate() {
    setEditing(null)
    form.reset(EMPTY)
    save.reset()
    setFormOpen(true)
  }

  function openEdit(profile: Profile) {
    setEditing(profile)
    form.reset({
      name: profile.name,
      adminBaseUrl: profile.adminBaseUrl,
      publicBaseUrl: profile.publicBaseUrl,
      adminUser: profile.adminUser,
      adminPassword: "",
    })
    save.reset()
    setFormOpen(true)
  }

  const mutationError = [setPrimary.error, diagnose.error, remove.error].find(Boolean)

  return (
    <>
      <PageHeader
        title="CouchDB"
        description="Vault를 올릴 CouchDB 서버 목록입니다. 주 서버는 새 Vault의 기본 대상이 됩니다."
        action={
          <Button onClick={openCreate}>
            <Plus aria-hidden /> CouchDB 추가
          </Button>
        }
      />

      {mutationError ? (
        <Alert variant="destructive" className="mb-4">
          <AlertTriangle aria-hidden />
          <AlertTitle>요청 실패</AlertTitle>
          <AlertDescription>
            {mutationError instanceof ApiError ? mutationError.message : String(mutationError)}
          </AlertDescription>
        </Alert>
      ) : null}

      {isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : !profiles?.length ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <Server className="text-muted-foreground size-8" aria-hidden />
            <p className="text-muted-foreground max-w-md text-sm">
              등록된 CouchDB가 없습니다. 서버를 추가하면 livesync에 필요한 설정이 함께 적용됩니다.
            </p>
            <Button onClick={openCreate}>
              <Plus aria-hidden /> 첫 CouchDB 추가
            </Button>
          </CardContent>
        </Card>
      ) : (
        <ul className="space-y-2">
          {profiles.map((p) => (
            <ProfileRow
              key={p.id}
              profile={p}
              busy={
                (setPrimary.isPending && setPrimary.variables === p.id) ||
                (diagnose.isPending && diagnose.variables === p.id)
              }
              onPrimary={() => setPrimary.mutate(p.id)}
              onDiagnose={() => diagnose.mutate(p.id)}
              onEdit={() => openEdit(p)}
              onRemove={() => setRemoving(p)}
            />
          ))}
        </ul>
      )}

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? `${editing.name} 수정` : "CouchDB 추가"}</DialogTitle>
            <DialogDescription>
              주소 두 개를 구분해서 입력하세요. 같은 값을 넣으면 데스크톱에서는 동기화되는데 휴대폰에서는 실패합니다.
            </DialogDescription>
          </DialogHeader>

          <form id="profile-form" onSubmit={form.handleSubmit((v) => save.mutate(v))}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="profile-name">이름</FieldLabel>
                <Input id="profile-name" autoComplete="off" placeholder="homelab" {...form.register("name")} />
                <FieldError errors={[form.formState.errors.name].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="profile-admin-url">CouchHub 연동용 주소</FieldLabel>
                <Input id="profile-admin-url" autoComplete="off" {...form.register("adminBaseUrl")} />
                <FieldDescription>CouchHub가 이 서버에 접근하는 주소입니다.</FieldDescription>
                <FieldError errors={[form.formState.errors.adminBaseUrl].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="profile-public-url">Obsidian 연동용 주소</FieldLabel>
                <Input
                  id="profile-public-url"
                  autoComplete="off"
                  placeholder="https://sync.example.com"
                  {...form.register("publicBaseUrl")}
                />
                <FieldDescription>Setup URI에 들어가는 주소이므로 휴대폰에서 접근 가능해야 합니다.</FieldDescription>
                <FieldError errors={[form.formState.errors.publicBaseUrl].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="profile-admin-user">관리자 계정</FieldLabel>
                <Input id="profile-admin-user" autoComplete="off" {...form.register("adminUser")} />
                <FieldError errors={[form.formState.errors.adminUser].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="profile-admin-password">관리자 비밀번호</FieldLabel>
                <Input
                  id="profile-admin-password"
                  type="password"
                  autoComplete="off"
                  placeholder={editing ? "비우면 기존 비밀번호 유지" : ""}
                  {...form.register("adminPassword")}
                />
                <FieldError errors={[form.formState.errors.adminPassword].filter(Boolean)} />
              </Field>
            </FieldGroup>

            <Alert className="mt-4">
              <AlertTriangle aria-hidden />
              <AlertTitle>livesync 설정이 이 서버에 적용됩니다</AlertTitle>
              <AlertDescription>
                CORS, 인증, 문서 크기 설정을 덮어씁니다. 시스템 데이터베이스가 없으면 함께 만듭니다.
              </AlertDescription>
            </Alert>

            {save.isError ? (
              <Alert variant="destructive" className="mt-4">
                <AlertTriangle aria-hidden />
                <AlertTitle>저장 실패</AlertTitle>
                <AlertDescription>
                  {save.error instanceof ApiError ? save.error.message : String(save.error)}
                </AlertDescription>
              </Alert>
            ) : null}
          </form>

          <DialogFooter>
            <Button variant="outline" onClick={() => setFormOpen(false)} disabled={save.isPending}>
              취소
            </Button>
            <Button type="submit" form="profile-form" disabled={save.isPending}>
              {save.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              {editing ? "저장" : "추가"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={result !== null} onOpenChange={(open) => !open && setResult(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{result?.profile.name ?? "CouchDB"} 상태</DialogTitle>
            <DialogDescription>
              CouchDB {result?.diagnosis.version} · 노드 {result?.diagnosis.nodeCount}개
            </DialogDescription>
          </DialogHeader>

          {result?.diagnosis.ready ? (
            <Alert>
              <CheckCircle2 aria-hidden />
              <AlertTitle>livesync 설정이 모두 적용되어 있습니다</AlertTitle>
              <AlertDescription>이 서버에 Vault를 만들 수 있습니다.</AlertDescription>
            </Alert>
          ) : (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>아직 맞지 않는 설정이 있습니다</AlertTitle>
              <AlertDescription>
                <ul className="mt-1 list-disc space-y-0.5 pl-4 text-xs">
                  {(result?.diagnosis.missingSystemDbs ?? []).map((db) => (
                    <li key={db}>시스템 데이터베이스 {db} 없음</li>
                  ))}
                  {(result?.diagnosis.checks ?? [])
                    .filter((c) => !c.matches)
                    .map((c) => (
                      <li key={`${c.section}.${c.key}`}>
                        [{c.section}] {c.key} — 현재 {c.present ? c.current : "없음"}
                      </li>
                    ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}

          {result?.diagnosis.singleNode === false ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>클러스터로 보입니다</AlertTitle>
              <AlertDescription>
                CouchHub는 요청을 받은 노드만 설정합니다. 나머지 노드는 직접 맞춰야 합니다.
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button onClick={() => setResult(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={removing !== null} onOpenChange={(open) => !open && setRemoving(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{removing?.name} 제거</DialogTitle>
            <DialogDescription>
              CouchHub 목록에서만 내립니다. CouchDB 서버와 그 안의 데이터베이스는 그대로 남습니다.
            </DialogDescription>
          </DialogHeader>

          {removing && removing.vaultCount > 0 ? (
            <Alert variant="destructive">
              <AlertTriangle aria-hidden />
              <AlertTitle>Vault {removing.vaultCount}개가 이 서버에 있습니다</AlertTitle>
              <AlertDescription>
                각 Vault를 다른 CouchDB로 옮기거나 삭제한 뒤에 제거할 수 있습니다.
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button variant="outline" onClick={() => setRemoving(null)} disabled={remove.isPending}>
              취소
            </Button>
            <Button
              variant="destructive"
              onClick={() => removing && remove.mutate(removing.id)}
              disabled={remove.isPending || (removing?.vaultCount ?? 0) > 0}
            >
              {remove.isPending ? <Loader2 className="animate-spin" aria-hidden /> : <Trash2 aria-hidden />}
              제거
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function ProfileRow({
  profile,
  busy,
  onPrimary,
  onDiagnose,
  onEdit,
  onRemove,
}: {
  profile: Profile
  busy: boolean
  onPrimary: () => void
  onDiagnose: () => void
  onEdit: () => void
  onRemove: () => void
}) {
  return (
    <li className="rounded-lg border p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate font-medium">{profile.name}</span>
            {profile.primary ? <Badge className="text-[10px]">주 서버</Badge> : null}
            {!profile.provisioned ? (
              <Badge variant="secondary" className="text-[10px]">
                설정 필요
              </Badge>
            ) : null}
          </div>
          <div className="text-muted-foreground truncate font-mono text-xs">{profile.adminBaseUrl}</div>
          <div className="text-muted-foreground truncate font-mono text-xs">{profile.publicBaseUrl}</div>
          <div className="text-muted-foreground mt-1 text-xs">Vault {profile.vaultCount}개</div>
        </div>

        <div className="flex shrink-0 flex-wrap gap-2">
          {!profile.primary ? (
            <Button variant="outline" size="sm" onClick={onPrimary} disabled={busy}>
              <Star aria-hidden /> 주 서버로
            </Button>
          ) : null}
          <Button variant="outline" size="sm" onClick={onDiagnose} disabled={busy}>
            {busy ? <Loader2 className="animate-spin" aria-hidden /> : <Stethoscope aria-hidden />}
            상태 확인
          </Button>
          <Button variant="ghost" size="icon" aria-label={`${profile.name} 수정`} onClick={onEdit}>
            <Pencil aria-hidden />
          </Button>
          <Button variant="ghost" size="icon" aria-label={`${profile.name} 제거`} onClick={onRemove}>
            <Trash2 aria-hidden />
          </Button>
        </div>
      </div>
    </li>
  )
}
