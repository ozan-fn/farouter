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

const Dashboard = () => {
  const [accounts, setAccounts] = useState<AccountStatus[]>([]);
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [rtkToggling, setRtkToggling] = useState(false);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(() => {
    Promise.all([
      fetch('/status').then((r) => r.json()),
      fetch('/api/rtk').then((r) => r.json()),
    ])
      .then(([accts, rtk]) => {
        setAccounts(accts as AccountStatus[]);
        setRtkEnabled((rtk as { rtkEnabled: boolean }).rtkEnabled);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchData();
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

  return (
    <section>
      <h1 className="mb-6 text-3xl font-bold">Dashboard</h1>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <>
          {/* Account status cards */}
          <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-3">
            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Active</div>
              <div className="mt-2 text-3xl font-bold text-green-400">{active}</div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Exhausted</div>
              <div className="mt-2 text-3xl font-bold text-yellow-400">{exhausted}</div>
            </div>

            <div className="border border-gray-700 bg-gray-800/40 p-6 transition hover:border-gray-600 hover:bg-gray-800/60">
              <div className="text-sm text-gray-400">Suspended</div>
              <div className="mt-2 text-3xl font-bold text-red-400">{suspended}</div>
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
