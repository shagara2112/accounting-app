import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import type { Me } from '../lib/api'
import { faqs, guideGroups, topicText, visibleTopics, type Faq, type GuideSection, type GuideTopic } from '../lib/guide'
import { Button, EmptyState, PageHeader, inputClass } from '../components/ui'

const FAQ = 'faq'

/** Menu Panduan: langkah memakai setiap menu, pencarian, dan pertanyaan umum. Tombol Panduan di
 *  bar atas membuka topik halaman yang sedang dibuka lewat ?topik=. */
export function GuidePage({ me }: { me: Me }) {
  const list = useMemo(() => visibleTopics(me), [me])
  const [params, setParams] = useSearchParams()
  const [query, setQuery] = useState('')
  const id = params.get('topik') ?? list[0]?.id
  const topic = list.find((t) => t.id === id)
  const showFaq = id === FAQ || !topic
  const topicIds = new Set(list.map((t) => t.id))
  const q = query.trim().toLowerCase()

  function open(next: string) {
    setQuery('')
    setParams(next === list[0]?.id ? {} : { topik: next }, { replace: false })
    window.scrollTo(0, 0)
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="print:hidden">
        <PageHeader
          title="Panduan"
          subtitle="Langkah memakai Lajur, disusun per menu. Tombol Panduan di kanan atas setiap halaman langsung membuka panduan halaman itu."
          actions={
            <Button variant="secondary" onClick={() => window.print()}>
              Cetak / PDF
            </Button>
          }
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-[250px_minmax(0,1fr)] print:hidden">
        <aside className="flex flex-col gap-3 lg:sticky lg:top-6 lg:max-h-[calc(100vh-3rem)] lg:self-start lg:overflow-y-auto lg:pb-2">
          <input
            type="search"
            aria-label="Cari panduan"
            placeholder="Cari, mis. retur, PPh 23, tutup buku"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className={`${inputClass} border-line-2`}
          />
          <select
            aria-label="Pilih topik panduan"
            value={showFaq ? FAQ : (topic?.id ?? FAQ)}
            onChange={(e) => open(e.target.value)}
            className={`${inputClass} cursor-pointer border-line-2 lg:hidden`}
          >
            {guideGroups.map((g) => {
              const items = list.filter((t) => t.group === g)
              return (
                items.length > 0 && (
                  <optgroup key={g} label={g}>
                    {items.map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.title}
                      </option>
                    ))}
                  </optgroup>
                )
              )
            })}
            <option value={FAQ}>Pertanyaan umum</option>
          </select>
          <nav aria-label="Topik panduan" className="hidden flex-col gap-0.5 lg:flex">
            {guideGroups.map((g) => {
              const items = list.filter((t) => t.group === g)
              if (items.length === 0) return null
              return (
                <div key={g} className="mt-3 flex flex-col gap-0.5 first:mt-0">
                  <p className="mb-0.5 px-3 text-xs font-semibold tracking-wide text-muted uppercase">{g}</p>
                  {items.map((t) => (
                    <TopicLink key={t.id} active={!q && !showFaq && t.id === topic?.id} onClick={() => open(t.id)}>
                      {t.title}
                    </TopicLink>
                  ))}
                </div>
              )
            })}
            <p className="mt-3 mb-0.5 px-3 text-xs font-semibold tracking-wide text-muted uppercase">Bantuan</p>
            <TopicLink active={!q && showFaq} onClick={() => open(FAQ)}>
              Pertanyaan umum
            </TopicLink>
          </nav>
        </aside>

        <div className="min-w-0">
          {q ? (
            <SearchResults q={q} list={list} topicIds={topicIds} onOpen={open} />
          ) : topic && !showFaq ? (
            <TopicView topic={topic} list={list} topicIds={topicIds} onOpen={open} />
          ) : (
            <FaqList items={faqs} list={list} topicIds={topicIds} onOpen={open} />
          )}
        </div>
      </div>

      {/* Versi cetak: semua topik yang bisa dibuka pengguna, lalu pertanyaan umum. */}
      <div className="hidden flex-col gap-8 print:flex">
        <h1 className="text-2xl font-bold">Panduan Lajur</h1>
        {list.map((t) => (
          <section key={t.id} className="break-inside-avoid-page">
            <h2 className="text-xl font-bold">{t.title}</h2>
            <p className="mb-3 text-sm text-muted">{t.summary}</p>
            {t.sections.map((s) => (
              <SectionBody key={s.title} section={s} />
            ))}
          </section>
        ))}
        <section>
          <h2 className="mb-3 text-xl font-bold">Pertanyaan umum</h2>
          {faqs.map((f) => (
            <div key={f.q} className="mb-3 break-inside-avoid">
              <p className="font-semibold">{f.q}</p>
              <p className="text-sm">{f.a}</p>
            </div>
          ))}
        </section>
      </div>
    </div>
  )
}

function TopicLink({ active, onClick, children }: { active: boolean; onClick: () => void; children: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={`min-h-9 cursor-pointer rounded-[8px] px-3 py-1.5 text-left text-sm transition-colors duration-150 ${active ? 'bg-accent-soft font-semibold text-accent' : 'text-ink-2 hover:bg-hover'}`}
    >
      {children}
    </button>
  )
}

function SectionBody({ section: s }: { section: GuideSection }) {
  return (
    <div className="flex flex-col gap-2.5">
      <h3 className="text-[15px] font-semibold">{s.title}</h3>
      {s.text && <p className="text-sm leading-relaxed text-ink-2">{s.text}</p>}
      {s.steps && (
        <ol className="flex flex-col gap-2">
          {s.steps.map((step, i) => (
            <li key={step} className="flex gap-3 text-sm leading-relaxed text-ink-2">
              <span className="num mt-px flex size-6 shrink-0 items-center justify-center rounded-full bg-accent-soft text-xs font-semibold text-accent">{i + 1}</span>
              <span>{step}</span>
            </li>
          ))}
        </ol>
      )}
      {s.points && (
        <ul className="flex list-disc flex-col gap-1.5 pl-5 text-sm leading-relaxed text-ink-2 marker:text-muted">
          {s.points.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
      {s.note && <p className="rounded-[10px] border border-line bg-hover/60 px-3.5 py-2.5 text-[13px] leading-relaxed text-ink-2">{s.note}</p>}
    </div>
  )
}

function TopicView({ topic, list, topicIds, onOpen }: { topic: GuideTopic; list: GuideTopic[]; topicIds: Set<string>; onOpen: (id: string) => void }) {
  const i = list.indexOf(topic)
  const prev = list[i - 1]
  const next = list[i + 1]
  const related = faqs.filter((f) => f.topic === topic.id)
  return (
    <article className="flex flex-col gap-5">
      <header className="flex flex-col gap-2">
        <p className="text-xs font-semibold tracking-wide text-muted uppercase">{topic.group}</p>
        <h2 className="text-[22px] font-bold tracking-tight">{topic.title}</h2>
        <p className="max-w-2xl text-sm text-muted">{topic.summary}</p>
        {topic.page && (
          <Link to={topic.page.to} className="self-start text-sm font-semibold text-accent hover:underline">
            Buka halaman {topic.page.label} →
          </Link>
        )}
      </header>
      {topic.sections.map((s) => (
        <section key={s.title} className="rounded-[14px] border border-line bg-surface p-5">
          <SectionBody section={s} />
        </section>
      ))}
      {related.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="text-[15px] font-semibold">Pertanyaan terkait</h3>
          <FaqItems items={related} list={list} topicIds={topicIds} onOpen={onOpen} hideTopicLink />
        </section>
      )}
      <nav aria-label="Topik sebelum dan sesudah" className="flex flex-wrap justify-between gap-3 border-t border-line pt-4 text-sm">
        {prev ? (
          <button type="button" onClick={() => onOpen(prev.id)} className="cursor-pointer text-left font-semibold text-accent hover:underline">
            ← {prev.title}
          </button>
        ) : (
          <span />
        )}
        {next ? (
          <button type="button" onClick={() => onOpen(next.id)} className="cursor-pointer text-right font-semibold text-accent hover:underline">
            {next.title} →
          </button>
        ) : (
          <button type="button" onClick={() => onOpen(FAQ)} className="cursor-pointer text-right font-semibold text-accent hover:underline">
            Pertanyaan umum →
          </button>
        )}
      </nav>
    </article>
  )
}

function FaqItems({ items, list, topicIds, onOpen, hideTopicLink }: { items: Faq[]; list: GuideTopic[]; topicIds: Set<string>; onOpen: (id: string) => void; hideTopicLink?: boolean }) {
  return (
    <div className="flex flex-col overflow-hidden rounded-[14px] border border-line bg-surface">
      {items.map((f) => {
        const topic = f.topic && topicIds.has(f.topic) ? list.find((t) => t.id === f.topic) : undefined
        return (
          <details key={f.q} className="group border-b border-line last:border-0">
            <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-3 px-5 py-3 text-sm font-semibold transition-colors duration-150 hover:bg-hover [&::-webkit-details-marker]:hidden">
              {f.q}
              <span aria-hidden="true" className="text-muted transition-transform duration-150 group-open:rotate-90">
                ›
              </span>
            </summary>
            <div className="flex flex-col gap-2 px-5 pb-4 text-sm leading-relaxed text-ink-2">
              <p>{f.a}</p>
              {topic && !hideTopicLink && (
                <button type="button" onClick={() => onOpen(topic.id)} className="cursor-pointer self-start font-semibold text-accent hover:underline">
                  Baca panduan {topic.title} →
                </button>
              )}
            </div>
          </details>
        )
      })}
    </div>
  )
}

function FaqList({ items, list, topicIds, onOpen }: { items: Faq[]; list: GuideTopic[]; topicIds: Set<string>; onOpen: (id: string) => void }) {
  return (
    <section className="flex flex-col gap-4">
      <header className="flex flex-col gap-2">
        <p className="text-xs font-semibold tracking-wide text-muted uppercase">Bantuan</p>
        <h2 className="text-[22px] font-bold tracking-tight">Pertanyaan umum</h2>
        <p className="max-w-2xl text-sm text-muted">Jawaban singkat untuk pertanyaan yang paling sering muncul. Klik pertanyaan untuk membuka jawabannya.</p>
      </header>
      <FaqItems items={items} list={list} topicIds={topicIds} onOpen={onOpen} />
    </section>
  )
}

function SearchResults({ q, list, topicIds, onOpen }: { q: string; list: GuideTopic[]; topicIds: Set<string>; onOpen: (id: string) => void }) {
  const words = q.split(/\s+/).filter(Boolean)
  const hit = (text: string) => words.every((w) => text.includes(w))
  const found = list.filter((t) => hit(topicText(t)))
  const faqFound = faqs.filter((f) => hit(`${f.q} ${f.a}`.toLowerCase()))
  if (found.length === 0 && faqFound.length === 0) {
    return <EmptyState title="Tidak ada panduan yang cocok" body="Coba kata lain, mis. nama menu (Penjualan, Pajak) atau nama dokumen (faktur, tagihan, jurnal)." />
  }
  return (
    <div className="flex flex-col gap-5">
      {found.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-[15px] font-semibold">Panduan ({found.length})</h2>
          <ul className="flex flex-col overflow-hidden rounded-[14px] border border-line bg-surface">
            {found.map((t) => (
              <li key={t.id} className="border-b border-line last:border-0">
                <button type="button" onClick={() => onOpen(t.id)} className="flex w-full cursor-pointer flex-col gap-0.5 px-5 py-3 text-left transition-colors duration-150 hover:bg-hover">
                  <span className="text-sm font-semibold">{t.title}</span>
                  <span className="text-[13px] text-muted">
                    {t.group} · {t.summary}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {faqFound.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-[15px] font-semibold">Pertanyaan umum ({faqFound.length})</h2>
          <FaqItems items={faqFound} list={list} topicIds={topicIds} onOpen={onOpen} />
        </section>
      )}
    </div>
  )
}
