import './main.css'

const root = document.documentElement
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches

// Theme: the inline head script sets data-theme before paint; the toggle cycles and saves.
document.querySelectorAll('[data-theme-toggle]').forEach((btn) => {
  btn.addEventListener('click', () => {
    const next = root.dataset.theme === 'dark' ? 'light' : 'dark'
    root.dataset.theme = next
    try { localStorage.setItem('recap-theme', next) } catch (e) { /* storage blocked */ }
  })
})

// Mobile nav
const navBtn = document.querySelector('[data-nav-toggle]')
const nav = document.querySelector('[data-nav]')
if (navBtn && nav) {
  navBtn.addEventListener('click', () => {
    const open = nav.classList.toggle('max-md:hidden') === false
    navBtn.setAttribute('aria-expanded', String(open))
  })
  nav.addEventListener('click', (e) => {
    if (e.target.closest('a') && matchMedia('(max-width: 767px)').matches) {
      nav.classList.add('max-md:hidden')
      navBtn.setAttribute('aria-expanded', 'false')
    }
  })
}

// Terminal typing: lines marked data-step appear in order; .cmd text is typed.
function typeTerminal(term) {
  const steps = [...term.querySelectorAll('[data-step]')]
  if (reduced || !steps.length) return
  const texts = steps.map((s) => {
    const c = s.querySelector('.cmd')
    const t = c ? c.textContent : ''
    if (c) c.textContent = ''
    s.style.visibility = 'hidden'
    return t
  })
  let i = 0
  const next = () => {
    if (i >= steps.length) return
    const s = steps[i]
    const c = s.querySelector('.cmd')
    const text = texts[i]
    i++
    s.style.visibility = 'visible'
    if (!c) return void setTimeout(next, 260)
    let n = 0
    c.classList.add('cursor')
    const tick = () => {
      c.textContent = text.slice(0, ++n)
      if (n < text.length) setTimeout(tick, 18)
      else { c.classList.remove('cursor'); setTimeout(next, 450) }
    }
    tick()
  }
  const start = () => next()
  if ('IntersectionObserver' in window) {
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting)) { io.disconnect(); start() }
    }, { threshold: 0.2 })
    io.observe(term)
  } else start()
}
document.querySelectorAll('[data-typing]').forEach(typeTerminal)

// Docs: highlight the section in view in every "On this page" list.
const toc = [...document.querySelectorAll('[data-toc] a[href^="#"]')]
if (toc.length && 'IntersectionObserver' in window) {
  const byId = new Map()
  toc.forEach((a) => {
    const id = a.getAttribute('href').slice(1)
    if (!byId.has(id)) byId.set(id, [])
    byId.get(id).push(a)
  })
  const set = (id) => toc.forEach((a) => {
    const on = a.getAttribute('href') === '#' + id
    on ? a.setAttribute('aria-current', 'true') : a.removeAttribute('aria-current')
  })
  const io = new IntersectionObserver((es) => {
    const hit = es.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0]
    if (hit) set(hit.target.id)
  }, { rootMargin: '-90px 0px -70% 0px' })
  byId.forEach((_, id) => { const el = document.getElementById(id); if (el) io.observe(el) })
  document.querySelectorAll('[data-toc-details] a').forEach((a) =>
    a.addEventListener('click', () => a.closest('details').removeAttribute('open')))
}
