import { useEffect, useState } from 'react';

const Settings = () => {
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [toggling, setToggling] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch('/api/rtk')
      .then((r) => r.json())
      .then((data) => {
        setRtkEnabled((data as { rtkEnabled: boolean }).rtkEnabled);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  const toggleRTK = async (val: boolean) => {
    setToggling(true);
    try {
      const res = await fetch('/api/rtk', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rtkEnabled: val }),
      });
      const data = (await res.json()) as { rtkEnabled: boolean };
      setRtkEnabled(data.rtkEnabled);
    } finally {
      setToggling(false);
    }
  };

  return (
    <section className="space-y-6">
      <h1 className="text-3xl font-bold">Settings</h1>

      {loading ? (
        <p className="text-gray-400">Loading...</p>
      ) : (
        <div className="space-y-4">
          {/* Server info */}
          <div className="border border-gray-800 bg-gray-900/30 p-5 transition hover:border-gray-700 hover:bg-gray-900/50">
            <h2 className="mb-1 text-sm font-semibold text-gray-300">Server</h2>
            <p className="text-sm text-gray-500">farouter · port 20180</p>
          </div>

          {/* RTK toggle */}
          <div className="border border-gray-800 bg-gray-900/30 p-5 transition hover:border-gray-700 hover:bg-gray-900/50">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-sm font-semibold text-gray-300">
                  RTK (Response Tool Kit)
                </h2>
                <p className="mt-1 text-xs text-gray-500">
                  Automatically compress tool output (git status, ls, test results, etc.) in
                  chat messages to reduce context usage and improve response quality.
                </p>
                <p className="mt-1 text-xs text-gray-500">
                  Affects all new chat completions. Disable if you need to see full raw tool
                  output.
                </p>
              </div>
            </div>
            <div className="mt-4">
              <button
                onClick={() => toggleRTK(!rtkEnabled)}
                disabled={toggling}
                className={`relative inline-flex h-6 w-11 items-center transition-colors focus:outline-none focus:ring-2 focus:ring-cyan-500 focus:ring-offset-2 focus:ring-offset-gray-900 ${
                  rtkEnabled ? 'bg-cyan-500' : 'bg-gray-600'
                }`}
              >
                <span
                  className={`inline-block h-4 w-4 transform bg-white transition-transform ${
                    rtkEnabled ? 'translate-x-6' : 'translate-x-1'
                  }`}
                />
              </button>
              <span className="ml-3 text-sm text-gray-400">
                {rtkEnabled ? 'Enabled' : 'Disabled'}
              </span>
              {toggling && <span className="ml-2 text-xs text-gray-500">updating...</span>}
            </div>
          </div>

          {/* Theme */}
          <div className="border border-gray-800 bg-gray-900/30 p-5 transition hover:border-gray-700 hover:bg-gray-900/50">
            <h2 className="mb-1 text-sm font-semibold text-gray-300">Theme</h2>
            <p className="text-sm text-gray-500">Dark mode only</p>
          </div>
        </div>
      )}
    </section>
  );
};

export default Settings;
