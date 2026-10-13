import { Navigate, Route, Routes } from 'react-router'
import { ApiError } from './lib/api'
import { useMe } from './lib/queries'
import { Shell } from './components/Shell'
import { Skeleton } from './components/ui'
import { Login } from './pages/Login'
import { Home } from './pages/Home'
import { Audit } from './pages/Audit'
import { Journals } from './pages/Journals'
import { JournalForm } from './pages/JournalForm'
import { AccountLedgerPage } from './pages/AccountLedger'
import { Worksheet } from './pages/Worksheet'
import { Accounts } from './pages/Accounts'
import { Periods } from './pages/Periods'
import { TradePage } from './pages/Trade'
import { TradeDocForm } from './pages/TradeDocForm'
import { SettlementForm } from './pages/SettlementForm'
import { CashPage } from './pages/Cash'
import { Contacts } from './pages/Contacts'
import { TaxPage } from './pages/Tax'

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
      return <Login />
    }
    return (
      <main className="flex min-h-screen items-center justify-center px-4 text-center">
        <p role="alert" className="max-w-md text-sm text-muted">
          Lajur tidak bisa dihubungi: {me.error.message}
        </p>
      </main>
    )
  }

  return (
    <Routes>
      <Route element={<Shell me={me.data} />}>
        <Route index element={<Home me={me.data} />} />
        <Route path="audit" element={<Audit me={me.data} />} />
        <Route path="jurnal" element={<Journals me={me.data} />} />
        <Route path="jurnal/baru" element={<JournalForm me={me.data} />} />
        <Route path="buku-besar" element={<AccountLedgerPage me={me.data} />} />
        <Route path="neraca-lajur" element={<Worksheet me={me.data} />} />
        <Route path="akun" element={<Accounts me={me.data} />} />
        <Route path="periode" element={<Periods me={me.data} />} />
        <Route path="penjualan" element={<TradePage key="sales" me={me.data} kind="sales" />} />
        <Route path="penjualan/baru" element={<TradeDocForm key="sales" me={me.data} kind="sales" />} />
        <Route path="penjualan/:id/ubah" element={<TradeDocForm key="sales-edit" me={me.data} kind="sales" />} />
        <Route path="penjualan/pelunasan/baru" element={<SettlementForm key="sales" me={me.data} kind="sales" />} />
        <Route path="pembelian" element={<TradePage key="purchase" me={me.data} kind="purchase" />} />
        <Route path="pembelian/baru" element={<TradeDocForm key="purchase" me={me.data} kind="purchase" />} />
        <Route path="pembelian/:id/ubah" element={<TradeDocForm key="purchase-edit" me={me.data} kind="purchase" />} />
        <Route path="pembelian/pembayaran/baru" element={<SettlementForm key="purchase" me={me.data} kind="purchase" />} />
        <Route path="kas" element={<CashPage me={me.data} />} />
        <Route path="kontak" element={<Contacts me={me.data} />} />
        <Route path="pajak" element={<TaxPage me={me.data} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}
