import {
  BellIcon,
  BookOpenIcon,
  BotIcon,
  ClipboardListIcon,
  FileJsonIcon,
  FlagIcon,
  FlaskConicalIcon,
  FrameIcon,
  HistoryIcon,
  InboxIcon,
  KeyRoundIcon,
  LayersIcon,
  LayoutGridIcon,
  LifeBuoyIcon,
  LogInIcon,
  RocketIcon,
  ShieldCheckIcon,
  ShuffleIcon,
  SmartphoneIcon,
  SparklesIcon,
  StoreIcon,
  TriangleAlertIcon,
  UsersIcon,
} from 'lucide-react'

import pages from 'virtual:pages'
import { sectionOf } from '@/section.js'

export const SECTION = sectionOf(import.meta.env.VITE_SECTION)

// Every page, in reading order, with the module that renders it and the
// mark it wears in the rail. The order is the rail's and the previous/next
// links'. Content loads when a page is opened, so the first paint carries
// one page and not twenty.
export const ICONS = {
  BookOpen: BookOpenIcon,
  Rocket: RocketIcon,
  FileJson: FileJsonIcon,
  Store: StoreIcon,
  KeyRound: KeyRoundIcon,
  Shuffle: ShuffleIcon,
  Frame: FrameIcon,
  ShieldCheck: ShieldCheckIcon,
  Bell: BellIcon,
  Bot: BotIcon,
  Layers: LayersIcon,
  TriangleAlert: TriangleAlertIcon,
  FlaskConical: FlaskConicalIcon,
  Flag: FlagIcon,
  Smartphone: SmartphoneIcon,
  LogIn: LogInIcon,
  ClipboardList: ClipboardListIcon,
  Users: UsersIcon,
  History: HistoryIcon,
  LayoutGrid: LayoutGridIcon,
  Inbox: InboxIcon,
  LifeBuoy: LifeBuoyIcon,
  Sparkles: SparklesIcon,
}

// The section's own pages and no others. The test is a build-time constant,
// so the branch not taken leaves no chunks behind in this bundle.
const modules =
  import.meta.env.VITE_SECTION === 'app'
    ? import.meta.glob('./content/app/*.mdx')
    : import.meta.glob('./content/*.mdx')

export const PAGES = pages.map((p, i) => ({
  ...p,
  n: i,
  Icon: ICONS[p.icon],
  load: modules[`./content/${p.file}.mdx`],
}))

// The first page of this section: where the logo goes.
export const HOME = PAGES[0].href

// Whether a link stays inside this build. One to the other section is a
// full page load, since the other site is its own bundle.
export function known(href) {
  return pageAt(href.split('#')[0]) !== null
}

export const GROUPS = [...new Set(PAGES.map((p) => p.group))].map((group) => ({
  title: group,
  pages: PAGES.filter((p) => p.group === group),
}))

export function pageAt(pathname) {
  const path = pathname === '/' ? '/' : pathname.replace(/\/+$/, '')
  return PAGES.find((p) => p.href === path) ?? null
}
