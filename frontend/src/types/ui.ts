export interface Column<T extends Record<string, unknown>> {
  key: keyof T | string
  label: string
}
