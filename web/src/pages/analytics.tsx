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
  authMethod?: string;
  region?: string;
}

interface UsageMetric {
  timestamp: number;
  activeCount: number;
  exhaustedCount: number;
  suspendedCount: number;
  totalRemaining: number;
  poolSize: number;
  requestCount: number;
  failedCount: number;
  tokensUsed: number;
  tokensGenerated: number;
}

const Analytics = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [metrics, setMetrics] = useState<UsageMetric[]>([]);

  const fetchData = useCallback(() => {
    Promise.all([
      fetch('/status').then((r) => r.json()),
      fetch('/api/analytics/metrics').then((r) => r.json()),
    ])
      .then(([accts, metricsData]) => {
        setAccounts(accts as AccountStatus[]);
        setMetrics(metricsData as UsageMetric[]);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const totalAccounts = accounts.length;
  const activeAccounts = accounts.filter((a) => !a.exhausted && !a.suspended);
  const poolAccounts = accounts.filter((a) => a.inPool);
  const totalRemaining = accounts.reduce((sum, a) => sum + (a.remaining || 0), 0);
  const avgRemaining = totalAccounts > 0 ? Math.round(totalRemaining / totalAccounts) : 0;

  const latestMetric = metrics.length > 0 ? metrics[metrics.length - 1] : null;
  const totalTokensUsed = latestMetric?.tokensUsed || 0;
  const totalTokensGenerated = latestMetric?.tokensGenerated || 0;
  const totalRequests = latestMetric?.requestCount || 0;
  const totalFailed = latestMetric?.failedCount || 0;

  const maxRemaining = metrics.length > 0 
    ? Math.max(...metrics.map((m) => m.totalRemaining), 1)
    : 1;

  const authMethodCounts = accounts.reduce((acc, a) => {
    const method = a.authMethod || 'unknown';
    acc[method] = (acc[method] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  const regionCounts = accounts.reduce((acc, a) => {
    const region = a.region || 'unknown';
    acc[region] = (acc[region] || 0) + 1;
    return acc;
  }, {} as Record<string, number>);

  return (
    <section>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-3xl font-bold">Analytics</h1>
        <button
          onClick={fetchData}
          className="border border-gray-700 bg-gray-800 px-4 py-2 text-sm text-gray-200 transition hover:border-gray-600 hover:bg-gray-700"
        >
          Refresh
        </button>
      </div>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Total Requests</div>
              <div className="mt-2 text-3xl font-bold text-cyan-400">{totalRequests.toLocaleString()}</div>
              <div className="mt-1 text-xs text-gray-500">
                {totalFailed} failed
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Tokens Used</div>
              <div className="mt-2 text-3xl font-bold text-blue-400">{totalTokensUsed.toLocaleString()}</div>
              <div className="mt-1 text-xs text-gray-500">
                Input tokens
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Tokens Generated</div>
              <div className="mt-2 text-3xl font-bold text-green-400">
                {totalTokensGenerated.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                Output tokens
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Total Tokens</div>
              <div className="mt-2 text-3xl font-bold text-purple-400">
                {(totalTokensUsed + totalTokensGenerated).toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                Combined usage
              </div>
            </div>
          </div>

          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Total Accounts</div>
              <div className="mt-2 text-2xl font-bold text-cyan-400">{totalAccounts}</div>
              <div className="mt-1 text-xs text-gray-500">
                {activeAccounts.length} active
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Pool Size</div>
              <div className="mt-2 text-2xl font-bold text-blue-400">{poolAccounts.length}</div>
              <div className="mt-1 text-xs text-gray-500">
                In rotation
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Total Remaining</div>
              <div className="mt-2 text-2xl font-bold text-green-400">
                {totalRemaining.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                Quota available
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <div className="text-sm text-gray-400">Avg per Account</div>
              <div className="mt-2 text-2xl font-bold text-purple-400">
                {avgRemaining.toLocaleString()}
              </div>
              <div className="mt-1 text-xs text-gray-500">
                Average quota
              </div>
            </div>
          </div>

          <div className="mb-6 border border-gray-700 bg-gray-800/40 p-6">
            <h2 className="mb-4 text-xl font-semibold">Usage Over Time</h2>
            <div className="h-64">
              {metrics.length > 0 ? (
                <div className="flex h-full items-end justify-between gap-1">
                  {metrics.map((m, i) => {
                    const height = (m.totalRemaining / maxRemaining) * 100;
                    return (
                      <div
                        key={i}
                        className="group relative flex-1"
                        style={{ minWidth: '8px' }}
                      >
                        <div className="flex h-full flex-col justify-end">
                          <div
                            className="w-full bg-cyan-500/60 transition-all group-hover:bg-cyan-400"
                            style={{ height: `${height}%` }}
                          />
                        </div>
                        <div className="pointer-events-none absolute bottom-full left-1/2 mb-2 hidden -translate-x-1/2 whitespace-nowrap border border-gray-700 bg-gray-900 px-2 py-1 text-xs group-hover:block">
                          <div className="text-gray-300">{m.totalRemaining.toLocaleString()} tokens</div>
                          <div className="text-gray-500">
                            {new Date(m.timestamp).toLocaleTimeString()}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div className="flex h-full items-center justify-center text-gray-500">
                  No data yet
                </div>
              )}
            </div>
            <div className="mt-2 text-xs text-gray-500">
              Last 30 data points (30s interval)
            </div>
          </div>

          <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <h2 className="mb-4 text-xl font-semibold">Auth Methods</h2>
              <div className="space-y-3">
                {Object.entries(authMethodCounts).map(([method, count]) => {
                  const pct = Math.round((count / totalAccounts) * 100);
                  return (
                    <div key={method}>
                      <div className="mb-1 flex justify-between text-sm">
                        <span className="text-gray-300">{method}</span>
                        <span className="text-gray-400">
                          {count} ({pct}%)
                        </span>
                      </div>
                      <div className="h-2 bg-gray-700">
                        <div
                          className="h-full bg-cyan-500"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6">
              <h2 className="mb-4 text-xl font-semibold">Regions</h2>
              <div className="space-y-3">
                {Object.entries(regionCounts).map(([region, count]) => {
                  const pct = Math.round((count / totalAccounts) * 100);
                  return (
                    <div key={region}>
                      <div className="mb-1 flex justify-between text-sm">
                        <span className="text-gray-300">{region}</span>
                        <span className="text-gray-400">
                          {count} ({pct}%)
                        </span>
                      </div>
                      <div className="h-2 bg-gray-700">
                        <div
                          className="h-full bg-blue-500"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          </div>

          <div className="border border-gray-700 bg-gray-800/40 p-6">
            <h2 className="mb-4 text-xl font-semibold">Account Status Breakdown</h2>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b border-gray-700">
                  <tr className="text-left">
                    <th className="pb-3 font-medium text-gray-400">Label</th>
                    <th className="pb-3 font-medium text-gray-400">Status</th>
                    <th className="pb-3 font-medium text-gray-400">Remaining</th>
                    <th className="pb-3 font-medium text-gray-400">Auth Method</th>
                    <th className="pb-3 font-medium text-gray-400">Region</th>
                    <th className="pb-3 font-medium text-gray-400">In Pool</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-800">
                  {accounts.map((acc) => {
                    let statusColor = 'text-green-400';
                    let statusText = 'Active';
                    if (acc.suspended) {
                      statusColor = 'text-red-400';
                      statusText = 'Suspended';
                    } else if (acc.exhausted) {
                      statusColor = 'text-yellow-400';
                      statusText = 'Exhausted';
                    }

                    return (
                      <tr key={acc.id} className="hover:bg-gray-800/40">
                        <td className="py-3 text-gray-200">{acc.label}</td>
                        <td className={`py-3 ${statusColor}`}>{statusText}</td>
                        <td className="py-3 text-gray-300">
                          {acc.remaining?.toLocaleString() || 0}
                        </td>
                        <td className="py-3 text-gray-400">{acc.authMethod || 'N/A'}</td>
                        <td className="py-3 text-gray-400">{acc.region || 'N/A'}</td>
                        <td className="py-3">
                          {acc.inPool ? (
                            <span className="text-cyan-400">Yes</span>
                          ) : (
                            <span className="text-gray-500">No</span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </section>
  );
};

export default Analytics;
