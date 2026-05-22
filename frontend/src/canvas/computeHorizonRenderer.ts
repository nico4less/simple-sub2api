type Tone = 'green' | 'violet' | 'cyan'

type HorizonNode = {
  t: number
  x: number
  y: number
  r: number
  phase: number
  tone: Tone
}

type CircuitTrace = {
  startX: number
  startY: number
  side: -1 | 1
  depth: number
  drop: number
  segment: number
  phase: number
  tone: Tone
}

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max)

function toneColor(tone: Tone, alpha: number) {
  if (tone === 'green') return `rgba(74, 138, 72, ${alpha})`
  if (tone === 'violet') return `rgba(58, 92, 158, ${alpha})`
  return `rgba(90, 74, 48, ${alpha})`
}

export function mountComputeHorizon(canvas: HTMLCanvasElement) {
  const ctx = canvas.getContext('2d', { alpha: true })
  if (!ctx) return () => undefined

  const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  let width = 0
  let height = 0
  let dpr = 1
  let nodes: HorizonNode[] = []
  let traces: CircuitTrace[] = []
  let rafId = 0

  const buildNodes = () => {
    const count: number = width < 760 ? 28 : 52
    const horizonY = height * 0.7
    const horizonHeight = height * 0.24
    nodes = Array.from({ length: count }, (_, index) => {
      const t = count === 1 ? 0 : index / (count - 1)
      const arc = Math.sin(t * Math.PI)
      return {
        t,
        x: width * (-0.08 + t * 1.16),
        y: horizonY + arc * -horizonHeight + (Math.random() - 0.5) * 40,
        r: 1.1 + Math.random() * 2.1,
        phase: Math.random() * Math.PI * 2,
        tone: Math.random() > 0.7 ? 'green' : Math.random() > 0.52 ? 'violet' : 'cyan'
      }
    })
  }

  const buildTraces = () => {
    const laneCount = width < 760 ? 14 : 24
    const cx = width * 0.5
    const baseY = height * 0.74
    const span = width * 0.86
    traces = Array.from({ length: laneCount }, (_, index) => {
      const side = index % 2 === 0 ? -1 : 1
      const depth = index / Math.max(laneCount - 1, 1)
      return {
        startX: cx + (0.08 + depth * 0.42) * span * side,
        startY: baseY + Math.sin(depth * Math.PI) * -48 + (index % 3) * 8,
        side,
        depth,
        drop: 28 + (index % 7) * 24 + depth * height * 0.1,
        segment: 24 + (index % 5) * 16,
        phase: Math.random() * Math.PI * 2,
        tone: index % 6 === 0 ? 'green' : index % 5 === 0 ? 'violet' : 'cyan'
      }
    })
  }

  const resize = () => {
    dpr = clamp(window.devicePixelRatio || 1, 1, 2)
    width = window.innerWidth
    height = Math.max(window.innerHeight, 720)
    canvas.width = Math.floor(width * dpr)
    canvas.height = Math.floor(height * dpr)
    canvas.style.width = `${width}px`
    canvas.style.height = `${height}px`
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    buildNodes()
    buildTraces()
  }

  const drawBackground = (time: number) => {
    const sky = ctx.createLinearGradient(0, 0, 0, height)
    sky.addColorStop(0, 'rgba(250, 247, 240, 0.96)')
    sky.addColorStop(0.52, 'rgba(245, 240, 230, 0.86)')
    sky.addColorStop(1, 'rgba(237, 232, 220, 0.96)')
    ctx.fillStyle = sky
    ctx.fillRect(0, 0, width, height)

    const glow = ctx.createRadialGradient(width * 0.52, height * 0.66, 0, width * 0.52, height * 0.66, width * 0.72)
    glow.addColorStop(0, 'rgba(90, 74, 48, 0.08)')
    glow.addColorStop(0.3, 'rgba(216, 208, 192, 0.08)')
    glow.addColorStop(0.58, 'rgba(58, 92, 158, 0.024)')
    glow.addColorStop(1, 'rgba(245, 240, 230, 0)')
    ctx.fillStyle = glow
    ctx.fillRect(0, 0, width, height)

    ctx.save()
    ctx.globalAlpha = 0.26
    for (let i = 0; i < 80; i += 1) {
      const x = (i * 157 + Math.sin(time * 0.00008 + i) * 12) % width
      const y = (i * 61) % (height * 0.48)
      const alpha = 0.08 + ((i % 7) / 7) * 0.14
      ctx.fillStyle = `rgba(58, 51, 40, ${alpha * 0.36})`
      ctx.fillRect(x, y, 1, 1)
    }
    ctx.restore()
  }

  const drawTraces = (time: number) => {
    ctx.save()
    ctx.globalCompositeOperation = 'multiply'
    traces.forEach((trace, index) => {
      const breath = 0.5 + Math.sin(time * 0.0012 + trace.phase) * 0.5
      const active = (time * 0.003 + index * 0.14) % 6 < 0.72
      const alpha = (0.04 + breath * 0.05 + (active ? 0.1 : 0)) * (1 - trace.depth * 0.28)
      const x0 = trace.startX
      const y0 = trace.startY
      const x1 = x0 + trace.side * trace.segment
      const y1 = y0 + trace.drop * 0.35
      const x2 = x1 - trace.side * trace.segment * 0.72
      const y2 = y1 + trace.drop * 0.42
      const x3 = x2 + trace.side * trace.segment * 1.36
      const y3 = y2 + trace.drop * 0.28

      ctx.beginPath()
      ctx.moveTo(x0, y0)
      ctx.lineTo(x1, y0)
      ctx.lineTo(x1, y1)
      ctx.lineTo(x2, y1)
      ctx.lineTo(x2, y2)
      ctx.lineTo(x3, y2)
      ctx.lineTo(x3, y3)
      ctx.strokeStyle = toneColor(trace.tone, alpha)
      ctx.lineWidth = active ? 1.1 : 0.8
      ctx.stroke()

      ;[[x0, y0], [x1, y1], [x2, y2], [x3, y3]].forEach(([x, y], dotIndex) => {
        if ((dotIndex + index) % 2 === 0) {
          ctx.beginPath()
          ctx.arc(x, y, active ? 2 : 1.4, 0, Math.PI * 2)
          ctx.fillStyle = toneColor(trace.tone, alpha + (active ? 0.14 : 0.04))
          ctx.fill()
        }
      })
    })
    ctx.restore()
  }

  const drawHorizon = (time: number) => {
    const cx = width * 0.5
    const cy = height * 1.06
    const rx = width * 0.82
    const ry = height * 0.42
    ctx.save()
    ctx.beginPath()
    ctx.ellipse(cx, cy, rx, ry, 0, Math.PI * 1.04, Math.PI * 1.96)
    const body = ctx.createLinearGradient(0, height * 0.42, 0, height)
    body.addColorStop(0, 'rgba(90, 74, 48, 0.08)')
    body.addColorStop(0.22, 'rgba(216, 208, 192, 0.18)')
    body.addColorStop(0.64, 'rgba(229, 223, 210, 0.58)')
    body.addColorStop(1, 'rgba(216, 208, 192, 0.82)')
    ctx.fillStyle = body
    ctx.fill()

    ctx.beginPath()
    ctx.ellipse(cx, cy, rx, ry, 0, Math.PI * 1.05, Math.PI * 1.95)
    ctx.strokeStyle = 'rgba(90, 74, 48, 0.22)'
    ctx.lineWidth = 1
    ctx.stroke()

    for (let i = 0; i < 3; i += 1) {
      const wave = (time * 0.00012 + i * 0.33) % 1
      ctx.beginPath()
      ctx.ellipse(cx, cy, rx * (0.78 + wave * 0.18), ry * (0.78 + wave * 0.18), 0, Math.PI * 1.08, Math.PI * 1.92)
      ctx.strokeStyle = `rgba(74, 138, 72, ${0.055 * (1 - wave)})`
      ctx.lineWidth = 1
      ctx.stroke()
    }
    ctx.restore()
  }

  const drawNetwork = (time: number) => {
    ctx.save()
    const drift = Math.sin(time * 0.00018) * 10
    const activeIndex = Math.floor((time * 0.006) % Math.max(nodes.length, 1))
    for (let i = 0; i < nodes.length - 1; i += 1) {
      const a = nodes[i]
      const b = nodes[i + 1]
      const activity = i === activeIndex || i === activeIndex - 1 ? 0.68 : 0.16
      ctx.beginPath()
      ctx.moveTo(a.x, a.y + drift * Math.sin(a.phase))
      ctx.lineTo(b.x, b.y + drift * Math.sin(b.phase))
      ctx.strokeStyle = `rgba(90, 74, 48, ${activity * 0.46})`
      ctx.lineWidth = i === activeIndex ? 1.3 : 0.8
      ctx.stroke()
    }

    nodes.forEach((node, index) => {
      const y = node.y + drift * Math.sin(node.phase + time * 0.00024)
      const breath = 0.58 + Math.sin(time * 0.0015 + node.phase) * 0.26
      const alpha = index === activeIndex ? 0.9 : 0.36 + breath * 0.22
      ctx.beginPath()
      ctx.arc(node.x, y, node.r + (index === activeIndex ? 1.4 : 0), 0, Math.PI * 2)
      ctx.fillStyle = toneColor(node.tone, alpha)
      ctx.fill()
      ctx.beginPath()
      ctx.arc(node.x, y, node.r + 6, 0, Math.PI * 2)
      ctx.strokeStyle = toneColor(node.tone, 0.08 + breath * 0.08)
      ctx.lineWidth = 1
      ctx.stroke()
    })
    ctx.restore()
  }

  const render = (time: number) => {
    ctx.clearRect(0, 0, width, height)
    drawBackground(time)
    drawHorizon(time)
    drawTraces(time)
    drawNetwork(time)
    if (!prefersReducedMotion) rafId = window.requestAnimationFrame(render)
  }

  resize()
  render(0)
  window.addEventListener('resize', resize, { passive: true })

  return () => {
    window.removeEventListener('resize', resize)
    window.cancelAnimationFrame(rafId)
  }
}
