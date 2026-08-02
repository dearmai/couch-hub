import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { profileLabel, type Profile } from "@/lib/profiles"

/**
 * Picks one of the registered CouchDB servers.
 *
 * The primary is labelled rather than merely preselected: with several servers
 * registered, "which one am I about to put this vault on" is the question the
 * operator actually has, and the default answering it silently is how a vault
 * ends up on the wrong host.
 */
export function ProfileSelect({
  id,
  value,
  onChange,
  profiles,
  /** Servers to leave out, e.g. the one a vault is already on. */
  exclude = [],
  placeholder = "선택하세요",
  disabled,
}: {
  id: string
  value: string
  onChange: (id: string) => void
  profiles: Profile[] | undefined
  exclude?: string[]
  placeholder?: string
  disabled?: boolean
}) {
  const options = (profiles ?? []).filter((p) => !exclude.includes(p.id))

  return (
    <Select
      value={value}
      // base-ui's Select yields null when cleared; the field is a string.
      onValueChange={(v) => onChange(v ?? "")}
      disabled={disabled}
    >
      <SelectTrigger id={id}>
        {/* The value is a profile id, and the trigger renders whatever the
            value is unless told otherwise - which would put a generated id in
            front of the operator instead of the server's name. */}
        <SelectValue placeholder={placeholder}>
          {(value: string | null) => {
            const selected = options.find((p) => p.id === value)
            return selected ? profileLabel(selected) : placeholder
          }}
        </SelectValue>
      </SelectTrigger>
      <SelectContent>
        {options.map((p) => (
          <SelectItem key={p.id} value={p.id}>
            {profileLabel(p)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
