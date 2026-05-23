export interface Column<T extends Record<string, unknown>> {
  key: keyof T | string
  label: string
  headerClass?: string
  cellClass?: string
  mobileLabel?: string
  mobileHidden?: boolean
}
