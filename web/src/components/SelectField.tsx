import { useId } from "react";

export interface Option {
  value: string;
  label: string;
}

/**
 * A labelled filter. It is the native select on purpose: the platform gives the
 * keyboard, the touch picker and the screen reader for free (docs/painel/design.md).
 */
export function SelectField({
  label,
  value,
  onChange,
  options,
  className = "",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: Option[];
  className?: string;
}) {
  const id = useId();
  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      <label htmlFor={id} className="text-[0.95em] font-semibold tracking-wide">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="min-h-11 rounded-field border border-line bg-paper px-3 text-ink outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}
