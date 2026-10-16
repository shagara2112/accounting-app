import { Navigate, Route, Routes } from 'react-router'
import { ApiError } from './lib/api'
import { useMe } from './lib/queries'
import { Shell } from './components/Shell'
import { Skeleton } from './components/ui'
import { Login } from './pages/Login'
import { Home } from './pages/Home'
import { Audit } from './pages/Audit'
import { Journals } from './pages/Journals'
import { JournalEdit, JournalForm } from './pages/JournalForm'
import { AccountLedgerPage } from './pages/AccountLedger'
import { Worksheet } from './pages/Worksheet'
import { Accounts } from './pages/Accounts'
import { Periods } from './pages/Periods'
import { TradePage } from './pages/Trade'
import { TradeDocForm } from './pages/TradeDocForm'
import { SettlementForm } from './pages/SettlementForm'
import { NoteForm } from './pages/NoteForm'
import { ImportsPage } from './pages/Imports'
import { BookSettings } from './pages/BookSettings'
import { CashPage } from './pages/Cash'
import { ReconList, ReconPage } from './pages/Reconciliation'
import { Contacts } from './pages/Contacts'
import { TaxPage } from './pages/Tax'
import { Products } from './pages/Products'
import { InventoryPage } from './pages/Inventory'
import { AdjustmentForm } from './pages/AdjustmentForm'
import { Projects } from './pages/Projects'
import { ProjectDetailPage } from './pages/ProjectDetail'
import { Reports } from './pages/Reports'
import { Consolidation } from './pages/Consolidation'
import { Setup } from './pages/Setup'
import { MfaRequired, Profile } from './pages/Profile'
import { Users } from './pages/Users'
import { Companies } from './pages/Companies'
import { SystemPage } from './pages/System'
import { useSetupStatus } from './lib/admin'

export function App() {
  const me = useMe()

  if (me.isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center" aria-busy="true">
        <Skeleton className="h-8 w-40" />
      </div>
    )
  }
  if (me.isError) {
    if (me.error instanceof ApiError && me.error.status === 401) {
      return <SignedOut />
    }
    return (
      <main className="flex min-h-screen items-center justify-center px-4 text-center">
        <p role="alert" className="max-w-md text-sm text-muted">
          Lajur tidak bisa dihubungi: {me.error.message}
        </p>
      </main>
    )
  }

  if (me.data.mfaSetupRequired) {
    return <MfaRequired me={me.data} />
  }

  return (
    <Routes>
      <Route element={<Shell me={me.data} />}>
        <Route index element={<Home me={me.data} />} />
        <Route path="audit" element={<Audit me={me.data} />} />
        <Route path="jurnal" element={<Journals me={me.data} />} />
        <Route path="jurnal/baru" element={<JournalForm me={me.data} />} />
        <Route path="jurnal/:id/ubah" element={<JournalEdit me={me.data} />} />
        <Route path="buku-besar" element={<AccountLedgerPage me={me.data} />} />
        <Route path="neraca-lajur" element={<Worksheet me={me.data} />} />
        <Route path="akun" element={<Accounts me={me.data} />} />
        <Route path="periode" element={<Periods me={me.data} />} />
        <Route path="penjualan" element={<TradePage key="sales" me={me.data} kind="sales" />} />
        <Route path="penjualan/baru" element={<TradeDocForm key="sales" me={me.data} kind="sales" />} />
        <Route path="penjualan/:id/ubah" element={<TradeDocForm key="sales-edit" me={me.data} kind="sales" />} />
        <Route path="penjualan/pelunasan/baru" element={<SettlementForm key="sales" me={me.data} kind="sales" />} />
        <Route path="penjualan/retur/baru" element={<NoteForm key="sales" me={me.data} kind="sales" />} />
        <Route path="pembelian" element={<TradePage key="purchase" me={me.data} kind="purchase" />} />
        <Route path="pembelian/baru" element={<TradeDocForm key="purchase" me={me.data} kind="purchase" />} />
        <Route path="pembelian/:id/ubah" element={<TradeDocForm key="purchase-edit" me={me.data} kind="purchase" />} />
        <Route path="pembelian/pembayaran/baru" element={<SettlementForm key="purchase" me={me.data} kind="purchase" />} />
        <Route path="pembelian/retur/baru" element={<NoteForm key="purchase" me={me.data} kind="purchase" />} />
        <Route path="kas" element={<CashPage me={me.data} />} />
        <Route path="kas/rekonsiliasi" element={<ReconList me={me.data} />} />
        <Route path="impor" element={<ImportsPage me={me.data} />} />
        <Route path="cabang" element={<BookSettings me={me.data} />} />
        <Route path="kas/rekonsiliasi/:id" element={<ReconPage me={me.data} />} />
        <Route path="kontak" element={<Contacts me={me.data} />} />
        <Route path="pajak" element={<TaxPage me={me.data} />} />
        <Route path="produk" element={<Products me={me.data} />} />
        <Route path="persediaan" element={<InventoryPage me={me.data} />} />
        <Route path="persediaan/baru" element={<AdjustmentForm me={me.data} />} />
        <Route path="proyek" element={<Projects me={me.data} />} />
        <Route path="proyek/:id" element={<ProjectDetailPage key="project" me={me.data} />} />
        <Route path="laporan" element={<Reports me={me.data} />} />
        <Route path="konsolidasi" element={<Consolidation me={me.data} />} />
        <Route path="profil" element={<Profile me={me.data} />} />
        <Route path="pengguna" element={<Users me={me.data} />} />
        <Route path="pt" element={<Companies me={me.data} />} />
        <Route path="sistem" element={<SystemPage me={me.data} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

/** Belum masuk: setup wizard bila instalasi belum punya pengguna, selain itu halaman login. */
function SignedOut() {
  const setup = useSetupStatus(true)
  if (setup.isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center" aria-busy="true">
        <Skeleton className="h-8 w-40" />
      </div>
    )
  }
  if (setup.data?.required) return <Setup status={setup.data} />
  return <Login />
}
