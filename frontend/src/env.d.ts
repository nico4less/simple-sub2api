/// <reference types="vite/client" />

declare module '@vue/runtime-core' {
  interface ComponentCustomProperties {
    $t: (key: string, values?: Record<string, unknown>) => string
  }
}

export {}
