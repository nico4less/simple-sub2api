<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { mountComputeHorizon } from '@/canvas/computeHorizonRenderer'

const canvasRef = ref<HTMLCanvasElement | null>(null)
let cleanup: (() => void) | undefined

onMounted(() => {
  if (!canvasRef.value) return
  cleanup = mountComputeHorizon(canvasRef.value)
})

onBeforeUnmount(() => {
  cleanup?.()
})
</script>

<template>
  <div class="xforce-space-bg" aria-hidden="true">
    <canvas ref="canvasRef" />
  </div>
</template>
