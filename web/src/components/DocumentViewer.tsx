import { useMemo, useState } from "react"
import DOMPurify from "dompurify"
import { marked } from "marked"
import { AlertTriangle, Code, Eye } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import type { DocumentContent } from "@/lib/documents"

/**
 * Shows a note's markdown before and after rendering.
 *
 * The rendered half is sanitised. Note content is not trusted input even though
 * it is "the user's own": a vault can receive documents from a zone peer, and a
 * script tag in a note would run inside the panel that holds every vault's
 * credentials.
 */
export function DocumentViewer({ content }: { content: DocumentContent }) {
  const [tab, setTab] = useState("rendered")

  const html = useMemo(() => {
    if (content.binary || !content.text) return ""
    const parsed = marked.parse(content.text, { async: false })
    return DOMPurify.sanitize(parsed, {
      // Obsidian notes routinely link out; keep anchors but nothing executable.
      FORBID_TAGS: ["style", "form", "input", "button"],
      FORBID_ATTR: ["style"],
    })
  }, [content.binary, content.text])

  if (content.binary) {
    return (
      <Alert>
        <AlertTriangle aria-hidden />
        <AlertTitle>바이너리 파일입니다</AlertTitle>
        <AlertDescription>이미지·PDF 등은 미리보기를 제공하지 않습니다.</AlertDescription>
      </Alert>
    )
  }

  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v ?? "rendered")}>
      <TabsList className="w-full">
        <TabsTrigger value="rendered" className="flex-1">
          <Eye aria-hidden /> 렌더링 후
        </TabsTrigger>
        <TabsTrigger value="raw" className="flex-1">
          <Code aria-hidden /> 원문
        </TabsTrigger>
      </TabsList>

      <TabsContent value="rendered" className="pt-3">
        {html ? (
          // Sanitised immediately above; the raw markdown never reaches innerHTML.
          <div className="guide-prose max-h-[55vh] overflow-auto rounded-md border p-4" dangerouslySetInnerHTML={{ __html: html }} />
        ) : (
          <p className="text-muted-foreground text-sm">내용이 비어 있습니다.</p>
        )}
      </TabsContent>

      <TabsContent value="raw" className="pt-3">
        <pre className="bg-muted max-h-[55vh] overflow-auto rounded-md p-4 font-mono text-xs whitespace-pre-wrap">
          {content.text || "(비어 있음)"}
        </pre>
      </TabsContent>
    </Tabs>
  )
}
