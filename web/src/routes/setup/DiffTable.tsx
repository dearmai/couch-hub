import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { Diagnosis } from "@/lib/setup"

/**
 * Shows every setting CouchHub is about to write, alongside what the server has
 * now. This is the whole point of the verify step: connecting an existing
 * CouchDB should never silently overwrite an operator's configuration.
 */
export function DiffTable({ diagnosis }: { diagnosis: Diagnosis }) {
  const missing = diagnosis.missingSystemDbs ?? []
  const changing = diagnosis.checks.filter((c) => !c.matches)

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant={changing.length > 0 ? "default" : "secondary"}>변경 {changing.length}개</Badge>
        <Badge variant="secondary">유지 {diagnosis.checks.length - changing.length}개</Badge>
        {missing.length > 0 ? <Badge variant="default">DB 생성 {missing.length}개</Badge> : null}
      </div>

      {missing.length > 0 ? (
        <p className="text-sm text-muted-foreground">
          생성할 시스템 데이터베이스: <span className="font-mono text-xs">{missing.join(", ")}</span>
        </p>
      ) : null}

      {/* The table is wide; let it scroll inside its own box rather than making
          the whole page scroll sideways on a phone. */}
      <div className="overflow-x-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[38%]">설정</TableHead>
              <TableHead>현재</TableHead>
              <TableHead>변경 후</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {diagnosis.checks.map((c) => (
              <TableRow key={`${c.section}.${c.key}`} className={c.matches ? "opacity-60" : undefined}>
                <TableCell className="align-top">
                  <div className="font-mono text-xs">
                    [{c.section}] {c.key}
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">{c.why}</div>
                </TableCell>
                <TableCell className="align-top font-mono text-xs break-all">
                  {c.present ? c.current : <span className="text-muted-foreground">(없음)</span>}
                </TableCell>
                <TableCell className="align-top font-mono text-xs break-all">
                  {c.matches ? <span className="text-muted-foreground">그대로</span> : c.value}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
