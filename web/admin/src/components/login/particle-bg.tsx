// =====================================================
// ParticleBackground - พื้นหลังอนุภาคเคลื่อนไหว
// Canvas-based particle animation for login page
// สร้าง particle จุด indigo/purple พร้อมเส้นเชื่อมต่อ
// ใช้ requestAnimationFrame สำหรับ smooth 60fps animation
// =====================================================

'use client'

import { useRef, useEffect } from 'react'

/**
 * Particle - โครงสร้างข้อมูลแต่ละอนุภาค
 * เก็บตำแหน่ง ความเร็ว ขนาด และสี
 */
interface Particle {
  /** ตำแหน่ง X / X position */
  x: number
  /** ตำแหน่ง Y / Y position */
  y: number
  /** ความเร็วแนวนอน / Horizontal velocity */
  vx: number
  /** ความเร็วแนวตั้ง / Vertical velocity */
  vy: number
  /** ขนาดรัศมี / Particle radius */
  radius: number
  /** สีอนุภาค (indigo หรือ purple) / Particle color */
  color: string
}

/**
 * ParticleBackground - Component พื้นหลังอนุภาคเคลื่อนไหว
 * แสดง ~80 particles บน canvas ที่เคลื่อนที่ช้าๆ
 * มีเส้นเชื่อมต่อระหว่าง particles ที่อยู่ใกล้กัน (< 120px)
 * Responsive - ปรับขนาดตาม parent container อัตโนมัติ
 */
export function ParticleBackground() {
  /** อ้างอิง canvas element / Canvas DOM reference */
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d')!
    if (!ctx) return

    // ===== ตั้งค่าขนาด canvas ให้เต็ม parent / Set canvas to fill parent =====
    let width = canvas.offsetWidth
    let height = canvas.offsetHeight
    canvas.width = width
    canvas.height = height

    // ===== สีอนุภาค / Particle colors (indigo & purple) =====
    const colors = ['#6366f1', '#8b5cf6']

    // ===== สร้างอนุภาค ~80 ตัว / Create ~80 particles =====
    const PARTICLE_COUNT = 80
    const particles: Particle[] = []

    for (let i = 0; i < PARTICLE_COUNT; i++) {
      particles.push({
        x: Math.random() * width,
        y: Math.random() * height,
        // ความเร็วช้าๆ สุ่มทิศทาง / Slow random velocity
        vx: (Math.random() - 0.5) * 0.5,
        vy: (Math.random() - 0.5) * 0.5,
        // ขนาดสุ่ม 1.5-3.5px / Random size
        radius: Math.random() * 2 + 1.5,
        // สุ่มสี indigo หรือ purple / Random color
        color: colors[Math.floor(Math.random() * colors.length)],
      })
    }

    // ===== ระยะทางสูงสุดที่จะวาดเส้นเชื่อม / Max connection distance =====
    const CONNECTION_DISTANCE = 120

    /** animationId สำหรับ cleanup / For cancelling animation on unmount */
    let animationId: number

    /**
     * animate - ฟังก์ชันวาด frame ถัดไป
     * เรียกตัวเองผ่าน requestAnimationFrame
     * 1. เคลียร์ canvas
     * 2. วาดเส้นเชื่อมระหว่าง particles ที่อยู่ใกล้กัน
     * 3. วาด particle แต่ละตัว
     * 4. อัพเดทตำแหน่ง + bounce ที่ขอบ
     */
    function animate() {
      ctx.clearRect(0, 0, width, height)

      // ===== วาดเส้นเชื่อม / Draw connecting lines =====
      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
          const dx = particles[i].x - particles[j].x
          const dy = particles[i].y - particles[j].y
          const dist = Math.sqrt(dx * dx + dy * dy)

          if (dist < CONNECTION_DISTANCE) {
            // ความโปร่งใสตามระยะทาง / Opacity based on distance
            const opacity = 0.15 * (1 - dist / CONNECTION_DISTANCE)
            ctx.strokeStyle = `rgba(99, 102, 241, ${opacity})`
            ctx.lineWidth = 1
            ctx.beginPath()
            ctx.moveTo(particles[i].x, particles[i].y)
            ctx.lineTo(particles[j].x, particles[j].y)
            ctx.stroke()
          }
        }
      }

      // ===== วาดและอัพเดท particle / Draw & update each particle =====
      for (const p of particles) {
        // วาดจุดกลม / Draw circle
        ctx.beginPath()
        ctx.arc(p.x, p.y, p.radius, 0, Math.PI * 2)
        ctx.fillStyle = p.color
        ctx.fill()

        // อัพเดทตำแหน่ง / Update position
        p.x += p.vx
        p.y += p.vy

        // สะท้อนที่ขอบ canvas / Bounce at canvas edges
        if (p.x < 0 || p.x > width) p.vx *= -1
        if (p.y < 0 || p.y > height) p.vy *= -1
      }

      animationId = requestAnimationFrame(animate)
    }

    // ===== เริ่ม animation / Start animation loop =====
    animate()

    // ===== จัดการ resize / Handle window resize =====
    function handleResize() {
      if (!canvas) return
      width = canvas.offsetWidth
      height = canvas.offsetHeight
      canvas.width = width
      canvas.height = height
    }
    window.addEventListener('resize', handleResize)

    // ===== Cleanup ตอน unmount / Cancel animation & remove listener =====
    return () => {
      cancelAnimationFrame(animationId)
      window.removeEventListener('resize', handleResize)
    }
  }, [])

  return (
    <canvas
      ref={canvasRef}
      className="absolute inset-0 w-full h-full"
      aria-hidden="true"
    />
  )
}
