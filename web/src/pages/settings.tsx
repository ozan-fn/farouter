import { useEffect, useState } from 'react';
import { Copy, Check } from 'lucide-react';

const Settings = () => {
  const [rtkEnabled, setRtkEnabled] = useState(true);
  const [toggling, setToggling] = useState(false);
  const [loading, setLoading] = useState(true);
  const [baseURL, setBaseURL] = useState('http://localhost:20180');
  const [apiKey, setApiKey] = useState('');
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    fetch('/api/rtk')
      .then((r) => r.json())
      .then((data) => {
        setRtkEnabled((data as { rtkEnabled: boolean }).rtkEnabled);
        setLoading(false);
      })
      .catch(() => setLoading(false));
    
    setBaseURL(window.location.origin);
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

  const generateConfig = () => {
    return JSON.stringify({
      "$schema": "https://opencode.ai/config.json",
      "lsp": true,
      "provider": {
        "Kafuu": {
          "models": {
            "kr/auto": { "name": "Auto", "limit": { "context": 200000, "output": 65536 } },
            "kr/auto-thinking": { "name": "Auto Thinking", "reasoning": true, "limit": { "context": 200000, "output": 65536 } },
            "kr/claude-haiku-4.5": { "name": "Claude Haiku 4.5", "limit": { "context": 200000, "output": 65536 } },
            "kr/claude-haiku-4.5-thinking": { "name": "Claude Haiku 4.5 Thinking", "reasoning": true, "limit": { "context": 200000, "output": 65536 } },
            "kr/claude-sonnet-4.5": { "name": "Claude Sonnet 4.5", "limit": { "context": 200000, "output": 65536 } },
            "kr/claude-sonnet-4.5-thinking": { "name": "Claude Sonnet 4.5 Thinking", "reasoning": true, "limit": { "context": 200000, "output": 65536 } }
          },
          "npm": "@ai-sdk/openai-compatible",
          "options": {
            "apiKey": apiKey || "your-api-key-here",
            "baseURL": `${baseURL}/v1`
          }
        }
      }
    }, null, 2);
  };

  const copyConfig = () => {
    navigator.clipboard.writeText(generateConfig());
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
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

          <div className="border border-gray-800 bg-gray-900/30 p-5 transition hover:border-gray-700 hover:bg-gray-900/50">
            <h2 className="mb-3 text-sm font-semibold text-gray-300">OpenCode Configuration</h2>
            <p className="mb-2 text-xs text-gray-500">
              Generate OpenCode config to use farouter as your AI provider
            </p>
            <p className="mb-4 text-xs text-gray-400">
              Path: <code className="bg-gray-950 px-1 py-0.5 text-cyan-400">~/.config/opencode/opencode.json</code>
            </p>
            
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-gray-400">Base URL</label>
                <input
                  type="text"
                  value={baseURL}
                  onChange={(e) => setBaseURL(e.target.value)}
                  className="w-full border border-gray-700 bg-gray-800 px-3 py-2 text-sm text-gray-200 focus:border-cyan-500 focus:outline-none"
                  placeholder="http://localhost:20180"
                />
              </div>
              
              <div>
                <label className="mb-1 block text-xs text-gray-400">API Key (optional)</label>
                <input
                  type="text"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  className="w-full border border-gray-700 bg-gray-800 px-3 py-2 text-sm text-gray-200 focus:border-cyan-500 focus:outline-none"
                  placeholder="your-api-key-here"
                />
              </div>

              <div>
                <label className="mb-1 block text-xs text-gray-400">Generated Config</label>
                <div className="relative">
                  <pre className="overflow-x-auto border border-gray-700 bg-gray-950 p-3 text-xs text-gray-300">
                    {generateConfig()}
                  </pre>
                  <button
                    onClick={copyConfig}
                    className="absolute right-2 top-2 flex items-center gap-1 border border-gray-700 bg-gray-800 px-2 py-1 text-xs text-gray-300 transition hover:border-cyan-500 hover:text-cyan-400"
                  >
                    {copied ? (
                      <>
                        <Check size={14} />
                        Copied
                      </>
                    ) : (
                      <>
                        <Copy size={14} />
                        Copy
                      </>
                    )}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      )}
    </section>
  );
};

export default Settings;
