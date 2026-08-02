import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "react-router"
import { AlertTriangle, Database, Loader2, Plus, PackagePlus } from "lucide-react"

import { AdoptVaultDialog } from "@/components/AdoptVaultDialog"
import { PageHeader } from "@/components/PageHeader"
import { ProfileSelect } from "@/components/ProfileSelect"
import { SetupCredentials } from "@/components/SetupCredentials"
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
import { ApiError, statusQuery } from "@/lib/api"
import { primaryProfile, profileName, profilesQuery } from "@/lib/profiles"
import {
  createVaultSchema,
  vaultsApi,
  vaultsQuery,
  type CreateVaultValues,
  type VaultWithCredentials,
} from "@/lib/vaults"

export default function Vaults() {
  const [createOpen, setCreateOpen] = useState(false)
  const [adoptOpen, setAdoptOpen] = useState(false)
  const [created, setCreated] = useState<VaultWithCredentials | null>(null)
  const queryClient = useQueryClient()

  const { data: vaults, isPending } = useQuery(vaultsQuery)
  const { data: status } = useQuery(statusQuery)
  const { data: profiles } = useQuery(profilesQuery)
  // With one server the choice is not a choice, so the field only appears once
  // there is something to choose between.
  const multipleServers = (profiles?.length ?? 0) > 1

  const form = useForm<CreateVaultValues>({
    resolver: zodResolver(createVaultSchema),
    defaultValues: { profileId: "", name: "", dbName: "" },
  })
  const profileId = form.watch("profileId")

  const create = useMutation({
    mutationFn: vaultsApi.create,
    onSuccess: async (data) => {
      setCreateOpen(false)
      setCreated(data)
      form.reset()
      await queryClient.invalidateQueries({ queryKey: ["vaults"] })
      await queryClient.invalidateQueries({ queryKey: ["status"] })
    },
  })

  return (
    <>
      <PageHeader
        title="Vault"
        description="Vault를 만들면 CouchDB 데이터베이스와 전용 계정이 생성되고 Setup URI가 발급됩니다."
        action={
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => setAdoptOpen(true)}>
              <PackagePlus aria-hidden /> 기존 DB 추가
            </Button>
            <Button onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden /> Vault 만들기
            </Button>
          </div>
        }
      />

      {status && !status.secretEnabled ? (
        <Alert variant="destructive" className="mb-4">
          <AlertTriangle aria-hidden />
          <AlertTitle>COUCHHUB_SECRET이 설정되지 않았습니다</AlertTitle>
          <AlertDescription>
            자격증명이 저장되지 않습니다. 지금 만드는 Vault는 Setup URI를 한 번만 볼 수 있고 재발급할 수 없습니다.
          </AlertDescription>
        </Alert>
      ) : null}

      {isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : !vaults?.length ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <Database className="text-muted-foreground size-8" aria-hidden />
            <p className="text-muted-foreground text-sm">아직 Vault가 없습니다.</p>
            <div className="flex flex-wrap justify-center gap-2">
              <Button onClick={() => setCreateOpen(true)}>
                <Plus aria-hidden /> 첫 Vault 만들기
              </Button>
              <Button variant="outline" onClick={() => setAdoptOpen(true)}>
                <PackagePlus aria-hidden /> 기존 DB 추가
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : (
        <ul className="space-y-2">
          {vaults.map((v) => (
            <li key={v.id}>
              <Link
                to={`/vaults/${v.id}`}
                className="hover:bg-accent/50 flex items-center justify-between gap-3 rounded-lg border p-4 transition-colors"
              >
                <div className="min-w-0">
                  <div className="truncate font-medium">{v.name}</div>
                  <div className="text-muted-foreground truncate font-mono text-xs">{v.dbName}</div>
                  {multipleServers ? (
                    <div className="text-muted-foreground truncate text-xs">{profileName(profiles, v.profileId)}</div>
                  ) : null}
                </div>
                <div className="flex shrink-0 gap-1">
                  {v.adopted ? (
                    <Badge variant="secondary" className="text-[10px]">
                      기존 DB
                    </Badge>
                  ) : null}
                  {v.e2eeDisabled ? (
                    <Badge variant="secondary" className="text-[10px]">
                      암호화 없음
                    </Badge>
                  ) : null}
                  {!v.secretsPersisted ? (
                    <Badge variant="secondary" className="text-[10px]">
                      재발급 불가
                    </Badge>
                  ) : null}
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <AdoptVaultDialog open={adoptOpen} onOpenChange={setAdoptOpen} onAdopted={setCreated} />

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Vault 만들기</DialogTitle>
            <DialogDescription>
              CouchDB 데이터베이스와 이 Vault만 접근할 수 있는 전용 계정이 함께 생성됩니다.
            </DialogDescription>
          </DialogHeader>

          <form id="create-vault" onSubmit={form.handleSubmit((v) => create.mutate(v))}>
            <FieldGroup>
              {multipleServers ? (
                <Field>
                  <FieldLabel htmlFor="vault-profile">CouchDB</FieldLabel>
                  <ProfileSelect
                    id="vault-profile"
                    value={profileId || (primaryProfile(profiles)?.id ?? "")}
                    onChange={(id) => form.setValue("profileId", id)}
                    profiles={profiles}
                  />
                  <FieldDescription>데이터베이스와 계정이 이 서버에 만들어집니다.</FieldDescription>
                </Field>
              ) : null}

              <Field>
                <FieldLabel htmlFor="vault-name">Vault 이름</FieldLabel>
                <Input id="vault-name" autoComplete="off" placeholder="업무 노트" {...form.register("name")} />
                <FieldDescription>Obsidian에서 쓰는 이름과 같을 필요는 없습니다.</FieldDescription>
                <FieldError errors={[form.formState.errors.name].filter(Boolean)} />
              </Field>

              <Field>
                <FieldLabel htmlFor="vault-db">데이터베이스 이름 (선택)</FieldLabel>
                <Input id="vault-db" autoComplete="off" placeholder="이름에서 자동 생성" {...form.register("dbName")} />
                <FieldDescription>
                  비워두면 Vault 이름에서 만듭니다. CouchDB는 소문자 ASCII만 허용하므로 한글 이름은 자동으로 치환됩니다.
                </FieldDescription>
                <FieldError errors={[form.formState.errors.dbName].filter(Boolean)} />
              </Field>
            </FieldGroup>

            {create.isError ? (
              <Alert variant="destructive" className="mt-4">
                <AlertTriangle aria-hidden />
                <AlertTitle>생성 실패</AlertTitle>
                <AlertDescription>
                  {create.error instanceof ApiError ? create.error.message : String(create.error)}
                </AlertDescription>
              </Alert>
            ) : null}
          </form>

          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)} disabled={create.isPending}>
              취소
            </Button>
            <Button type="submit" form="create-vault" disabled={create.isPending}>
              {create.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              만들기
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={created !== null} onOpenChange={(open) => !open && setCreated(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{created?.vault.name} 준비 완료</DialogTitle>
            <DialogDescription>Obsidian에서 Setup URI를 불러오면 동기화가 시작됩니다.</DialogDescription>
          </DialogHeader>

          {created ? (
            <SetupCredentials credentials={created.credentials} persisted={created.secretsPersisted} />
          ) : null}

          <DialogFooter>
            <Button onClick={() => setCreated(null)}>닫기</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
