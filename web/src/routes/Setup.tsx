import { useState } from "react"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router"
import { AlertTriangle, ArrowLeft, ArrowRight, CheckCircle2, Circle, Database, Loader2 } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ApiError, statusQuery } from "@/lib/api"
import { connectSchema, setupApi, type ApplyResponse, type ConnectValues, type Diagnosis } from "@/lib/setup"
import { GuideStep } from "@/routes/setup/GuideStep"
import { DiffTable } from "@/routes/setup/DiffTable"

type Step = "guide" | "connect" | "verify" | "done"

const STEPS: { id: Step; label: string }[] = [
  { id: "guide", label: "가이드" },
  { id: "connect", label: "연결" },
  { id: "verify", label: "확인" },
  { id: "done", label: "완료" },
]

export default function Setup() {
  const [step, setStep] = useState<Step>("guide")
  const [diagnosis, setDiagnosis] = useState<Diagnosis | null>(null)
  const [applied, setApplied] = useState<ApplyResponse | null>(null)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  // The container usually knows its own CouchDB: compose passes the same values
  // that created it. Only the password is left to type - the API does not hand
  // that out, and the panel it would be handed out through has no login.
  const { data: status } = useQuery(statusQuery)
  const defaults = status?.setupDefaults

  const form = useForm<ConnectValues>({
    resolver: zodResolver(connectSchema),
    defaultValues: {
      name: defaults?.name || "homelab",
      adminBaseUrl: defaults?.adminBaseUrl || "http://couchdb:5984",
      publicBaseUrl: defaults?.publicBaseUrl || "",
      adminUser: defaults?.adminUser || "admin",
      adminPassword: "",
    },
  })

  const diagnose = useMutation({
    mutationFn: setupApi.diagnose,
    onSuccess: (data) => {
      setDiagnosis(data)
      setStep("verify")
    },
  })

  const apply = useMutation({
    mutationFn: setupApi.apply,
    onSuccess: async (data) => {
      setApplied(data)
      setDiagnosis(data.diagnosis)
      setStep("done")
      await queryClient.invalidateQueries({ queryKey: ["status"] })
    },
  })

  const pending = diagnose.isPending || apply.isPending

  return (
    <div className="min-h-svh px-4 py-8">
      <div className="mx-auto w-full max-w-3xl space-y-6">
        <header className="space-y-3 text-center">
          <Database className="mx-auto size-8" aria-hidden />
          <h1 className="text-2xl font-semibold tracking-tight">CouchHub 설치</h1>
          <ol className="flex flex-wrap items-center justify-center gap-x-2 gap-y-1 text-sm">
            {STEPS.map((s, i) => {
              const currentIndex = STEPS.findIndex((x) => x.id === step)
              const done = i < currentIndex
              const active = i === currentIndex
              return (
                <li key={s.id} className="flex items-center gap-2">
                  {i > 0 ? <span className="text-muted-foreground/50">›</span> : null}
                  <span
                    className={
                      active ? "font-medium text-foreground" : done ? "text-foreground/70" : "text-muted-foreground"
                    }
                  >
                    {done ? (
                      <CheckCircle2 className="mr-1 inline size-3.5" aria-hidden />
                    ) : (
                      <Circle className="mr-1 inline size-3.5" aria-hidden />
                    )}
                    {s.label}
                  </span>
                </li>
              )
            })}
          </ol>
        </header>

        {step === "guide" ? <GuideStep onNext={() => setStep("connect")} /> : null}

        {step === "connect" ? (
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>CouchDB 연결</CardTitle>
              <CardDescription>
                주소 두 개를 구분해서 입력하세요. 같은 값을 넣으면 데스크톱에서는 동기화되는데 휴대폰에서는 실패합니다.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form
                id="connect-form"
                onSubmit={form.handleSubmit((values) => diagnose.mutate(values))}
                className="space-y-6"
              >
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="name">프로필 이름</FieldLabel>
                    <Input id="name" autoComplete="off" {...form.register("name")} />
                    <FieldError errors={[form.formState.errors.name].filter(Boolean)} />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="adminBaseUrl">CouchHub 연동용 주소</FieldLabel>
                    <Input id="adminBaseUrl" placeholder="http://couchdb:5984" autoComplete="off" {...form.register("adminBaseUrl")} />
                    <FieldDescription>
                      CouchHub가 CouchDB를 관리할 때 쓰는 주소입니다. 보통 컨테이너 내부망 주소라 외부에서는 접근되지 않습니다.
                    </FieldDescription>
                    <FieldError errors={[form.formState.errors.adminBaseUrl].filter(Boolean)} />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="publicBaseUrl">Obsidian 연동용 주소</FieldLabel>
                    <Input id="publicBaseUrl" placeholder="https://sync.example.com" autoComplete="off" {...form.register("publicBaseUrl")} />
                    <FieldDescription>
                      Setup URI에 들어가는 주소입니다. 휴대폰에서 접근 가능해야 합니다.
                    </FieldDescription>
                    <FieldError errors={[form.formState.errors.publicBaseUrl].filter(Boolean)} />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="adminUser">관리자 계정</FieldLabel>
                    <Input id="adminUser" autoComplete="username" {...form.register("adminUser")} />
                    <FieldError errors={[form.formState.errors.adminUser].filter(Boolean)} />
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="adminPassword">관리자 비밀번호</FieldLabel>
                    <Input
                      id="adminPassword"
                      type="password"
                      autoComplete="current-password"
                      {...form.register("adminPassword")}
                    />
                    <FieldError errors={[form.formState.errors.adminPassword].filter(Boolean)} />
                  </Field>
                </FieldGroup>

                {diagnose.isError ? <ErrorAlert error={diagnose.error} /> : null}

                <div className="flex flex-wrap justify-between gap-2">
                  <Button type="button" variant="outline" onClick={() => setStep("guide")} disabled={pending}>
                    <ArrowLeft aria-hidden /> 이전
                  </Button>
                  <Button type="submit" disabled={pending}>
                    {diagnose.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
                    설정 확인
                    <ArrowRight aria-hidden />
                  </Button>
                </div>
              </form>
            </CardContent>
          </Card>
        ) : null}

        {step === "verify" && diagnosis ? (
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>적용 전 확인</CardTitle>
              <CardDescription>
                CouchDB {diagnosis.version} · 노드 {diagnosis.nodeCount}개
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {!diagnosis.singleNode ? (
                <Alert variant="destructive">
                  <AlertTriangle aria-hidden />
                  <AlertTitle>클러스터가 감지되었습니다</AlertTitle>
                  <AlertDescription>
                    CouchHub는 요청을 처리한 노드 하나만 설정합니다. 나머지 노드는 직접 동일하게 맞춰야 합니다.
                  </AlertDescription>
                </Alert>
              ) : null}

              {diagnosis.ready ? (
                <Alert>
                  <CheckCircle2 aria-hidden />
                  <AlertTitle>이미 livesync 규격에 맞게 설정되어 있습니다</AlertTitle>
                  <AlertDescription>적용을 눌러도 값은 바뀌지 않고 프로필만 저장됩니다.</AlertDescription>
                </Alert>
              ) : null}

              <DiffTable diagnosis={diagnosis} />

              {apply.isError ? <ErrorAlert error={apply.error} /> : null}

              <div className="flex flex-wrap justify-between gap-2">
                <Button type="button" variant="outline" onClick={() => setStep("connect")} disabled={pending}>
                  <ArrowLeft aria-hidden /> 이전
                </Button>
                <Button type="button" onClick={() => apply.mutate(form.getValues())} disabled={pending}>
                  {apply.isPending ? <Loader2 className="animate-spin" aria-hidden /> : null}
                  적용
                </Button>
              </div>
            </CardContent>
          </Card>
        ) : null}

        {step === "done" && applied ? (
          <Card>
            <CardHeader>
              <CardTitle role="heading" aria-level={2}>설치 완료</CardTitle>
              <CardDescription>CouchDB가 obsidian-livesync 규격으로 설정되었습니다.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <ul className="space-y-1 text-sm">
                {applied.steps.map((s) => (
                  <li key={s.step} className="flex items-center gap-2">
                    <CheckCircle2 className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                    <span className="font-mono text-xs">{s.step}</span>
                    {s.skipped ? (
                      <Badge variant="secondary" className="text-[10px]">
                        이미 적용됨
                      </Badge>
                    ) : null}
                  </li>
                ))}
              </ul>
              <Button type="button" className="w-full" onClick={() => navigate("/vaults")}>
                Vault 만들기 <ArrowRight aria-hidden />
              </Button>
            </CardContent>
          </Card>
        ) : null}
      </div>
    </div>
  )
}

function ErrorAlert({ error }: { error: unknown }) {
  const message = error instanceof ApiError ? error.message : String(error)
  return (
    <Alert variant="destructive">
      <AlertTriangle aria-hidden />
      <AlertTitle>실패</AlertTitle>
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  )
}
