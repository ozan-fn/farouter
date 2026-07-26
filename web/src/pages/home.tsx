const Home = () => {
  return (
    <section>
      <h1 className="mb-4 text-3xl font-bold">farouter</h1>
      <p className="mb-6 text-gray-400">
        OpenAI-compatible proxy for the Kiro executor. Route requests, rotate accounts,
        and stream responses.
      </p>
      <div className="border border-gray-800 bg-gray-900/30 p-6">
        <h2 className="mb-3 text-lg font-semibold text-cyan-400">Endpoints</h2>
        <ul className="space-y-2 text-sm text-gray-400">
          <li><code className="text-cyan-300">POST /v1/chat/completions</code> — Chat completions</li>
          <li><code className="text-cyan-300">GET /status</code> — Account status</li>
          <li><code className="text-cyan-300">POST /accounts/reset</code> — Reset accounts</li>
        </ul>
      </div>
    </section>
  );
};

export default Home;
