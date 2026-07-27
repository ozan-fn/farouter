import { useCallback, useEffect, useState } from 'react';

interface AccountStatus {
  id: string;
  label: string;
  remaining: number;
  exhausted: boolean;
  suspended: boolean;
  resetAt: string;
  hasToken: boolean;
  inPool: boolean;
}

interface Stats {
  tokensUsed: number;
  tokensGenerated: number;
  requestCount: number;
  failedCount: number;
}

const Dashboard = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [stats, setStats] = useState<Stats | null>(null);
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [rtkToggling, setRtkToggling] = useState(false);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(() => {
    Promise.all([
      fetch('/status').then((r) => r.json()),
      fetch('/api/rtk').then((r) => r.json()),
      fetch('/api/analytics/metrics').then((r) => r.json()),
    ])
      .then(([accts, rtk, metrics]) => {
        setAccounts(accts as AccountStatus[]);
        setRtkEnabled((rtk as { rtkEnabled: boolean }).rtkEnabled);
        
        if (metrics && metrics.length > 0) {
          const latest = metrics[metrics.length - 1];
          setStats({
            tokensUsed: latest.tokensUsed || 0,
            tokensGenerated: latest.tokensGenerated || 0,
            requestCount: latest.requestCount || 0,
            failedCount: latest.failedCount || 0,
          });
        }
        
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchData();
    const interval = setInterval(fetchData, 30000);
    return () => clearInterval(interval);
  }, [fetchData]);

  const toggleRTK = async () => {
    setRtkToggling(true);
    try {
      const res = await fetch('/api/rtk', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rtkEnabled: !rtkEnabled }),
      });
      const data = (await res.json()) as { rtkEnabled: boolean };
      setRtkEnabled(data.rtkEnabled);
    } finally {
      setRtkToggling(false);
    }
  };

  const active = accounts.filter((a) => !a.exhausted && !a.suspended).length;
  const exhausted = accounts.filter((a) => a.exhausted && !a.suspended).length;
  const suspended = accounts.filter((a) => a.suspended).length;
  const totalQuota = accounts.reduce((sum, a) => sum + (a.remaining || 0), 0);
  const poolSize = accounts.filter((a) => a.inPool).length;
  const totalAccounts = accounts.length;
  const totalTokens = (stats?.tokensUsed || 0) + (stats?.tokensGenerated || 0);
  const successRate = stats?.requestCount 
    ? (((stats.requestCount - stats.failedCount) / stats.requestCount) * 100).toFixed(1)
    : '0';

  const getHealthStatus = () => {
    if (suspended > 0) return { text: 'Degraded', color: 'text-red-400', bg: 'bg-red-500/10' };
    if (exhausted > totalAccounts / 2) return { text: 'Warning', color: 'text-yellow-400', bg: 'bg-yellow-500/10' };
    if (active > 0) return { text: 'Healthy', color: 'text-green-400', bg: 'bg-green-500/10' };
    return { text: 'Unknown', color: 'text-gray-400', bg: 'bg-gray-500/10' };
  };

  const health = getHealthStatus();

  return (
    <section>
      <h1 className="mb-6 text-3xl font-bold">Overview</h1>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Quota Available</div>
              <div className="mt-2 text-3xl font-bold text-cyan-400">
                {totalQuota.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                {totalAccounts} total accounts
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Pool Status</div>
              <div className="mt-2 text-3xl font-bold text-green-400">{poolSize} / 3</div>
              <div className="mt-1 text-xs text-gray-500">Active rotation</div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Total Requests</div>
              <div className="mt-2 text-3xl font-bold text-blue-400">
                {stats ? stats.requestCount.toLocaleString() : '0'}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                {successRate}% success rate
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Total Tokens</div>
              <div className="mt-2 text-3xl font-bold text-purple-400">
                {totalTokens.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                {stats ? `${stats.tokensUsed.toLocaleString()} in / ${stats.tokensGenerated.toLocaleString()} out` : 'N/A'}
              </div>
            </div>
          </div>

          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-3">
            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="flex items-center justify-between">
                <div className="text-sm text-gray-400">Active</div>
                <div className="text-2xl font-bold text-green-400">{active}</div>
              </div>
              <div className="mt-2 h-2 bg-gray-700">
                <div 
                  className="h-full bg-green-500"
                  style={{ width: `${totalAccounts > 0 ? (active / totalAccounts) * 100 : 0}%` }}
                />
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="flex items-center justify-between">
                <div className="text-sm text-gray-400">Exhausted</div>
                <div className="text-2xl font-bold text-yellow-400">{exhausted}</div>
              </div>
              <div className="mt-2 h-2 bg-gray-700">
                <div 
                  className="h-full bg-yellow-500"
                  style={{ width: `${totalAccounts > 0 ? (exhausted / totalAccounts) * 100 : 0}%` }}
                />
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="flex items-center justify-between">
                <div className="text-sm text-gray-400">Suspended</div>
                <div className="text-2xl font-bold text-red-400">{suspended}</div>
              </div>
              <div className="mt-2 h-2 bg-gray-700">
                <div 
                  className="h-full bg-red-500"
                  style={{ width: `${totalAccounts > 0 ? (suspended / totalAccounts) * 100 : 0}%` }}
                />
              </div>
            </div>
          </div>

          {/* RTK status card */}
          <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-sm text-gray-400">RTK (Response Tool Kit)</div>
                <div className="mt-1 text-xs text-gray-500">
                  Compress tool output in chat messages to save context
                </div>
              </div>
              <button
                onClick={toggleRTK}
                disabled={rtkToggling}
                className={`relative inline-flex h-6 w-11 items-center transition-colors focus:outline-none focus:ring-2 focus:ring-cyan-500 focus:ring-offset-2 focus:ring-offset-gray-800 ${
                  rtkEnabled ? 'bg-cyan-500' : 'bg-gray-600'
                }`}
              >
                <span
                  className={`inline-block h-4 w-4 transform bg-white transition-transform ${
                    rtkEnabled ? 'translate-x-6' : 'translate-x-1'
                  }`}
                />
              </button>
            </div>
            {rtkToggling && (
              <p className="mt-2 text-xs text-gray-500">Toggling...</p>
            )}
          </div>
        </>
      )}
    </section>
  );
};

export default Dashboard;
