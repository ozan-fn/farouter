import { useCallback, useEffect, useState } from 'react';
import { RefreshCw, Heart, CheckCircle, AlertTriangle, XCircle, Info } from 'lucide-react';

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

interface StatusLog {
  timestamp: number;
  message: string;
  type: 'info' | 'warning' | 'error' | 'success';
}

const Monitoring = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [logs, setLogs] = useState<StatusLog[]>([]);
  const [autoRefresh, setAutoRefresh] = useState(true);

  const addLog = useCallback((message: string, type: StatusLog['type']) => {
    setLogs((prev) => [
      { timestamp: Date.now(), message, type },
      ...prev.slice(0, 99),
    ]);
  }, []);

  const fetchData = useCallback(() => {
    Promise.all([
      fetch('/status').then((r) => r.json()),
      fetch('/api/analytics/logs').then((r) => r.json()),
    ])
      .then(([accts, logsData]) => {
        setAccounts(accts as AccountStatus[]);
        setLogs(logsData as StatusLog[]);
        setLoading(false);
      })
      .catch((err) => {
        addLog(`Failed to fetch data: ${err.message}`, 'error');
        setLoading(false);
      });
  }, [addLog]);

  useEffect(() => {
    fetchData();
  }, []);

  useEffect(() => {
    if (!autoRefresh) return;
    const interval = setInterval(fetchData, 10000);
    return () => clearInterval(interval);
  }, [autoRefresh, fetchData]);

  const active = accounts.filter((a) => !a.exhausted && !a.suspended).length;
  const exhausted = accounts.filter((a) => a.exhausted && !a.suspended).length;
  const suspended = accounts.filter((a) => a.suspended).length;
  const poolSize = accounts.filter((a) => a.inPool).length;

  const getHealthStatus = () => {
    if (suspended > 0) return { text: 'Degraded', color: 'text-red-400' };
    if (exhausted > accounts.length / 2) return { text: 'Warning', color: 'text-yellow-400' };
    if (active > 0) return { text: 'Healthy', color: 'text-green-400' };
    return { text: 'Unknown', color: 'text-gray-400' };
  };

  const health = getHealthStatus();

  return (
    <section>
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-3xl font-bold">Monitoring</h1>
        <div className="flex gap-2">
          <button
            onClick={() => setAutoRefresh(!autoRefresh)}
            className={`flex items-center gap-2 border px-4 py-2 text-sm transition ${
              autoRefresh
                ? 'border-cyan-500 bg-cyan-500/20 text-cyan-400'
                : 'border-gray-700 bg-gray-800 text-gray-400 hover:border-gray-600'
            }`}
          >
            <Heart size={16} />
            Auto-refresh {autoRefresh ? 'ON' : 'OFF'}
          </button>
          <button
            onClick={fetchData}
            className="flex items-center gap-2 border border-gray-700 bg-gray-800 px-4 py-2 text-sm text-gray-200 transition hover:border-gray-600 hover:bg-gray-700"
          >
            <RefreshCw size={16} />
            Refresh Now
          </button>
        </div>
      </div>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <>
          <div className="mb-6 border border-gray-700 bg-gray-800/40 p-6">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-xl font-semibold">System Health</h2>
              <span className={`text-2xl font-bold ${health.color}`}>{health.text}</span>
            </div>
            <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
              <div>
                <div className="text-sm text-gray-400">Active</div>
                <div className="mt-1 text-2xl font-bold text-green-400">{active}</div>
              </div>
              <div>
                <div className="text-sm text-gray-400">Exhausted</div>
                <div className="mt-1 text-2xl font-bold text-yellow-400">{exhausted}</div>
              </div>
              <div>
                <div className="text-sm text-gray-400">Suspended</div>
                <div className="mt-1 text-2xl font-bold text-red-400">{suspended}</div>
              </div>
              <div>
                <div className="text-sm text-gray-400">Pool Size</div>
                <div className="mt-1 text-2xl font-bold text-cyan-400">{poolSize}</div>
              </div>
            </div>
          </div>

          <div className="mb-6 border border-gray-700 bg-gray-800/40 p-6">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-xl font-semibold">Active Pool</h2>
              <span className="text-sm text-gray-400">{poolSize} / 3 slots</span>
            </div>
            <div className="space-y-3">
              {accounts
                .filter((a) => a.inPool)
                .map((acc) => {
                  let statusColor = 'border-green-500 bg-green-500/10';
                  if (acc.suspended) statusColor = 'border-red-500 bg-red-500/10';
                  else if (acc.exhausted) statusColor = 'border-yellow-500 bg-yellow-500/10';

                  return (
                    <div
                      key={acc.id}
                      className={`border p-4 ${statusColor}`}
                    >
                      <div className="flex items-center justify-between">
                        <div>
                          <div className="font-medium text-gray-200">{acc.label}</div>
                          <div className="mt-1 text-sm text-gray-400">
                            {acc.remaining?.toLocaleString() || 0} tokens remaining
                          </div>
                        </div>
                        <div className="text-right">
                          {acc.suspended && (
                            <span className="text-sm text-red-400">Suspended</span>
                          )}
                          {!acc.suspended && acc.exhausted && (
                            <span className="text-sm text-yellow-400">Exhausted</span>
                          )}
                          {!acc.suspended && !acc.exhausted && (
                            <span className="text-sm text-green-400">Active</span>
                          )}
                          {acc.resetAt && (
                            <div className="mt-1 text-xs text-gray-500">
                              Resets: {new Date(acc.resetAt).toLocaleString()}
                            </div>
                          )}
                        </div>
                      </div>
                    </div>
                  );
                })}
              {poolSize === 0 && (
                <p className="text-center text-gray-500">No accounts in pool</p>
              )}
            </div>
          </div>

          <div className="border border-gray-700 bg-gray-800/40 p-6">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-xl font-semibold">Activity Log</h2>
              <button
                onClick={() => {
                  fetch('/api/analytics/logs', { method: 'DELETE' })
                    .then(() => setLogs([]))
                    .catch((err) => console.error('Failed to clear logs:', err));
                }}
                className="text-sm text-gray-400 hover:text-gray-300"
              >
                Clear
              </button>
            </div>
            <div className="max-h-96 space-y-2 overflow-y-auto">
              {logs.length === 0 && (
                <p className="text-center text-gray-500">No activity logged yet</p>
              )}
              {logs.map((log, i) => {
                let typeColor = 'text-gray-400';
                let TypeIcon = Info;
                if (log.type === 'error') {
                  typeColor = 'text-red-400';
                  TypeIcon = XCircle;
                } else if (log.type === 'warning') {
                  typeColor = 'text-yellow-400';
                  TypeIcon = AlertTriangle;
                } else if (log.type === 'success') {
                  typeColor = 'text-green-400';
                  TypeIcon = CheckCircle;
                } else if (log.type === 'info') {
                  typeColor = 'text-cyan-400';
                  TypeIcon = Info;
                }

                return (
                  <div
                    key={i}
                    className="flex items-start gap-3 border-b border-gray-800 pb-2 last:border-0"
                  >
                    <TypeIcon size={16} className={typeColor} />
                    <div className="flex-1">
                      <div className="text-sm text-gray-300">{log.message}</div>
                      <div className="mt-1 text-xs text-gray-500">
                        {new Date(log.timestamp).toLocaleString()}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </>
      )}
    </section>
  );
};

export default Monitoring;
