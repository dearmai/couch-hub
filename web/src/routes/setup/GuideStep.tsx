import { useQuery } from "@tanstack/react-query"
import { marked } from "marked"
import { ArrowRight } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * Renders the CouchDB install guide the server embeds.
 *
 * dangerouslySetInnerHTML is safe here specifically because the markdown is a
 * compile-time constant baked into the Go binary (internal/httpapi/guide), not
 * anything a user or a remote server can influence. Do not point this at
 * arbitrary content without sanitising it first.
 */
export function GuideStep({ onNext }: { onNext: () => void }) {
  const { data, isPending, isError } = useQuery({
    queryKey: ["setup", "guide"],
    queryFn: async () => {
      const res = await fetch("/api/setup/guide")
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      return marked.parse(await res.text(), { async: false })
    },
    staleTime: Infinity,
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle role="heading" aria-level={2}>CouchDB 준비</CardTitle>
        <CardDescription>
          이미 CouchDB를 운영 중이라면 이 단계를 건너뛰고 바로 연결하세요.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        ) : isError ? (
          <p className="text-sm text-muted-foreground">가이드를 불러오지 못했습니다.</p>
        ) : (
          <div
            className="guide-prose max-h-[50vh] overflow-y-auto rounded-md border p-4"
            dangerouslySetInnerHTML={{ __html: data }}
          />
        )}

        <Button type="button" className="w-full" onClick={onNext}>
          연결 단계로 <ArrowRight aria-hidden />
        </Button>
      </CardContent>
    </Card>
  )
}
