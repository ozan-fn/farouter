import { useCallback, useEffect, useState } from 'react';
import { RefreshCw, Coins, CheckCircle, AlertTriangle, XCircle } from 'lucide-react';

interface AccountStatus {
  id: string;
  label: string;
  remaining: number;
  exhausted: boolean;
  suspended: boolean;
  resetAt: string;
  hasToken: boolean;
  inPool: boolean;
  authMethod?: string;
  region?: string;
}

const Quotas = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(() => {
    fetch('/status')
      .then((r) => r.json())
      .then((accts) => {
        setAccounts(accts as AccountStatus[]);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 10000);
    return () => clearInterval(interval);
  }, [fetchData]);

  const totalQuota = accounts.reduce((sum, a) => sum + (a.remaining || 0), 0);
  const active = accounts.filter((a) => !a.exhausted && !a.suspended).length;
  const exhausted = accounts.filter((a) => a.exhausted && !a.suspended).length;
  const suspended = accounts.filter((a) => a.suspended).length;

  return (
    <section>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-3xl font-bold">Quota Tracker</h1>
        <button
          onClick={fetchData}
          className="flex items-center gap-2 border border-gray-700 bg-gray-800 px-4 py-2 text-sm text-gray-200 transition hover:border-gray-600 hover:bg-gray-700"
        >
          <RefreshCw size={16} />
          Refresh
        </button>
      </div>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-4">
            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="flex items-center justify-between mb-2">
                <div className="text-sm text-gray-400">Total Quota</div>
                <Coins className="text-cyan-400" size={20} />
              </div>
              <div className="text-3xl font-bold text-cyan-400">
                {totalQuota.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                {accounts.length} accounts
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="flex items-center justify-between mb-2">
                <div className="text-sm text-gray-400">Suspended</div>
                <XCircle className="text-red-400" size={20} />
              </div>
              <div className="text-3xl font-bold text-red-400">{suspended}</div>
              <div className="mt-1 text-xs text-gray-500">
                Needs attention
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="flex items-center justify-between mb-2">
                <div className="text-sm text-gray-400">Active</div>
                <CheckCircle className="text-green-400" size={20} />
              </div>
              <div className="text-3xl font-bold text-green-400">{active}</div>
              <div className="mt-1 text-xs text-gray-500">
                {accounts.length > 0 ? Math.round((active / accounts.length) * 100) : 0}%
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="flex items-center justify-between mb-2">
                <div className="text-sm text-gray-400">Exhausted</div>
                <AlertTriangle className="text-yellow-400" size={20} />
              </div>
              <div className="text-3xl font-bold text-yellow-400">{exhausted}</div>
              <div className="mt-1 text-xs text-gray-500">
                Waiting for reset
              </div>
            </div>
          </div>

          <div className="border border-gray-700 bg-gray-800/40">
            <div className="border-b border-gray-700 p-6">
              <h2 className="text-xl font-semibold">Account Quotas</h2>
            </div>
            <div className="p-6">
              <div className="space-y-4">
                {accounts
                  .sort((a, b) => {
                    if (a.suspended !== b.suspended) return a.suspended ? -1 : 1;
                    if (a.exhausted !== b.exhausted) return b.exhausted ? 1 : -1;
                    return 0;
                  })
                  .map((acc) => {
                  const maxQuota = 100000;
                  const quotaPercent = Math.min((acc.remaining / maxQuota) * 100, 100);
                  
                  let statusColor = 'text-green-400';
                  let barColor = 'bg-green-500';
                  let statusText = 'Active';
                  
                  if (acc.suspended) {
                    statusColor = 'text-red-400';
                    barColor = 'bg-red-500';
                    statusText = 'Suspended';
                  } else if (acc.exhausted) {
                    statusColor = 'text-yellow-400';
                    barColor = 'bg-yellow-500';
                    statusText = 'Exhausted';
                  } else if (quotaPercent < 20) {
                    statusColor = 'text-orange-400';
                    barColor = 'bg-orange-500';
                  }

                  return (
                    <div
                      key={acc.id}
                      className="border border-gray-700 bg-gray-800/20 p-4 transition hover:bg-gray-800/40"
                    >
                      {acc.inPool && (
                        <div className="mb-2">
                          <span className="rounded bg-cyan-500/20 px-2 py-1 text-xs text-cyan-400 inline-block">
                            In Pool
                          </span>
                        </div>
                      )}
                      <div className="mb-3 flex items-start justify-between">
                        <div className="flex-1">
                          <div className="flex items-center gap-3">
                            <div className="text-lg font-semibold text-gray-200">
                              {acc.label}
                            </div>
                            <span className={`text-sm ${statusColor}`}>{statusText}</span>
                          </div>
                          <div className="mt-1 flex gap-4 text-xs text-gray-500">
                            {acc.authMethod && <span>Auth: {acc.authMethod}</span>}
                            {acc.region && <span>Region: {acc.region}</span>}
                          </div>
                        </div>
                        <div className="text-right">
                          <div className="text-2xl font-bold text-gray-200">
                            {acc.remaining.toLocaleString()}
                          </div>
                          <div className="text-xs text-gray-500">tokens</div>
                        </div>
                      </div>
                      
                      <div className="mb-2 h-2 bg-gray-700 rounded-full overflow-hidden">
                        <div
                          className={`h-full ${barColor} transition-all`}
                          style={{ width: `${quotaPercent}%` }}
                        />
                      </div>
                      
                      {acc.resetAt && (
                        <div className="text-xs text-gray-500">
                          Resets: {new Date(acc.resetAt).toLocaleString()}
                        </div>
                      )}
                    </div>
                  );
                  })}
              </div>
            </div>
          </div>
        </>
      )}
    </section>
  );
};

export default Quotas;
