import { useEffect, useState } from 'react';

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
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch('/status')
      .then((res) => res.json())
      .then((data) => {
        setAccounts(data);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  const active = accounts.filter((a) => !a.exhausted && !a.suspended).length;
  const exhausted = accounts.filter((a) => a.exhausted && !a.suspended).length;
  const suspended = accounts.filter((a) => a.suspended).length;

  return (
    <section>
      <h1 className="mb-6 text-3xl font-bold">Dashboard</h1>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          <div className="rounded-lg border border-gray-700 bg-gray-800 p-6">
            <div className="text-sm text-gray-400">Active</div>
            <div className="mt-2 text-3xl font-bold text-green-400">{active}</div>
          </div>

          <div className="rounded-lg border border-gray-700 bg-gray-800 p-6">
            <div className="text-sm text-gray-400">Exhausted</div>
            <div className="mt-2 text-3xl font-bold text-yellow-400">{exhausted}</div>
          </div>

          <div className="rounded-lg border border-gray-700 bg-gray-800 p-6">
            <div className="text-sm text-gray-400">Suspended</div>
            <div className="mt-2 text-3xl font-bold text-red-400">{suspended}</div>
          </div>
        </div>
      )}
    </section>
  );
};

export default Dashboard;
