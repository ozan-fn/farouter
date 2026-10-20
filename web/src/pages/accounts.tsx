import { useCallback, useEffect, useState } from 'react';
import { RefreshCw, Plus, Trash2, Users, ShieldCheck } from 'lucide-react';

interface AccountStatus {
  id: string;
  label: string;
  remaining: number;
  exhausted: boolean;
  suspended: boolean;
  invalid: boolean;
  resetAt: string;
  hasToken: boolean;
  inPool: boolean;
  authMethod?: string;
}

interface AddResult {
  added: { id: string; label: string }[];
  failed: { token: string; error: string }[];
}

const Accounts = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [tokens, setTokens] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<AddResult | null>(null);
  const [error, setError] = useState('');
  const [checking, setChecking] = useState<string | null>(null);
  const [checkResult, setCheckResult] = useState<Record<string, string>>({});

  const fetchAccounts = useCallback(() => {
    fetch('/status')
      .then((r) => r.json())
      .then((accts) => setAccounts(accts as AccountStatus[]))
      .catch(() => setError('Gagal memuat daftar akun'));
  }, []);

  useEffect(() => {
    fetchAccounts();
  }, [fetchAccounts]);

  const submit = async () => {
    setBusy(true);
    setError('');
    setResult(null);
    try {
      const res = await fetch('/accounts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refreshTokens: tokens }),
      });
      if (!res.ok) {
        setError(`HTTP ${res.status}`);
        return;
      }
      const data = (await res.json()) as AddResult;
      setResult(data);
      if (data.added.length > 0) setTokens('');
      fetchAccounts();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  const check = async (id: string) => {
    setChecking(id);
    setError('');
    try {
      const res = await fetch(`/accounts/${encodeURIComponent(id)}/check`, { method: 'POST' });
      if (!res.ok) {
        setCheckResult((prev) => ({ ...prev, [id]: `HTTP ${res.status}` }));
        return;
      }
      const data = await res.json();
      setCheckResult((prev) => ({ ...prev, [id]: data.status || 'ok' }));
      fetchAccounts();
    } catch (e) {
      setCheckResult((prev) => ({ ...prev, [id]: String(e) }));
    } finally {
      setChecking(null);
    }
  };

  const remove = async (id: string) => {
    if (!confirm('Hapus akun ini dari pool?')) return;
    setError('');
    const res = await fetch(`/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' });
    if (!res.ok) {
      setError(`Hapus gagal: HTTP ${res.status}`);
      return;
    }
    fetchAccounts();
  };

  const lineCount = tokens.split('\n').filter((l) => l.trim() !== '').length;

  return (
    <section>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-3xl font-bold">Accounts</h1>
        <button
          onClick={fetchAccounts}
          className="flex items-center gap-2 border border-gray-700 bg-gray-800 px-4 py-2 text-sm text-gray-200 transition hover:border-gray-600 hover:bg-gray-700"
        >
          <RefreshCw size={16} />
          Refresh
        </button>
      </div>

      <div className="mb-6 border border-gray-700 bg-gray-800/40 p-6">
        <h2 className="mb-3 flex items-center gap-2 text-lg font-semibold">
          <Plus size={18} /> Tambah akun
        </h2>
        <p className="mb-3 text-sm text-gray-400">
          Tempel refresh token saja — satu per baris. Field lain (id, label, authMethod, machineId,
          profileArn) diisi otomatis. Tiap token divalidasi ke Kiro dulu.
        </p>
        <textarea
          value={tokens}
          onChange={(e) => setTokens(e.target.value)}
          rows={5}
          spellCheck={false}
          placeholder={'aorAAAA...\naorAAAA...'}
          className="w-full border border-gray-700 bg-gray-950 p-3 font-mono text-xs text-gray-200 placeholder-gray-500 focus:border-cyan-500 focus:outline-none"
        />
        <div className="mt-3 flex items-center justify-between">
          <span className="text-xs text-gray-500">{lineCount} token terdeteksi</span>
          <button
            onClick={submit}
            disabled={busy || lineCount === 0}
            className="flex items-center gap-2 bg-cyan-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-cyan-500 disabled:opacity-40"
          >
            <Plus size={16} />
            {busy ? 'Menambahkan...' : 'Tambah'}
          </button>
        </div>

        {error !== '' && <p className="mt-3 border-l-2 border-red-500 pl-3 text-sm text-red-400">{error}</p>}

        {result && result.added.length > 0 && (
          <div className="mt-3">
            <p className="text-sm text-green-400">
              {result.added.length} akun ditambahkan: {result.added.map((a) => a.label).join(', ')}
            </p>
          </div>
        )}
        {result && result.failed.length > 0 && (
          <ul className="mt-2 space-y-1">
            {result.failed.map((f, i) => (
              <li key={i} className="border-l-2 border-yellow-500 pl-3 text-xs text-yellow-400">
                <span className="font-mono">{f.token}</span> — {f.error}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="border border-gray-700">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-gray-700 bg-gray-800/40 text-gray-400">
            <tr>
              <th className="px-4 py-2 font-medium">Label</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Sisa</th>
              <th className="px-4 py-2 font-medium">Auth</th>
              <th className="px-4 py-2 font-medium">Pool</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-800">
            {accounts.map((a) => {
              let statusColor = 'text-green-400';
              let statusText = 'Active';
              if (a.suspended) {
                statusColor = 'text-red-400';
                statusText = 'Suspended';
              } else if (a.invalid) {
                statusColor = 'text-red-400';
                statusText = 'Invalid token';
              } else if (a.exhausted) {
                statusColor = 'text-yellow-400';
                statusText = 'Exhausted';
              }
              return (
                <tr key={a.id || a.label}>
                  <td className="px-4 py-2">{a.label}</td>
                  <td className={`px-4 py-2 ${statusColor}`}>{statusText}</td>
                  <td className="px-4 py-2 text-gray-300">{a.remaining}</td>
                  <td className="px-4 py-2 text-gray-400">{a.authMethod || '-'}</td>
                  <td className="px-4 py-2 text-gray-400">{a.inPool ? 'yes' : 'standby'}</td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex items-center justify-end gap-3">
                      {checkResult[a.id] && (
                        <span className="text-xs text-gray-400">{checkResult[a.id]}</span>
                      )}
                      <button
                        onClick={() => check(a.id)}
                        disabled={checking === a.id}
                        title="Cek status akun sekarang"
                        className="text-gray-500 transition hover:text-cyan-400 disabled:opacity-40"
                      >
                        <ShieldCheck size={16} />
                      </button>
                      <button
                        onClick={() => remove(a.id)}
                        title="Hapus akun"
                        className="text-gray-500 transition hover:text-red-400"
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
            {accounts.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-gray-500">
                  <Users className="mx-auto mb-2" size={20} />
                  Belum ada akun
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
};

export default Accounts;
