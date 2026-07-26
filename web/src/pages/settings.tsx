const Settings = () => {
  return (
    <section className="space-y-6">
      <h1 className="text-3xl font-bold">Settings</h1>
      <div className="space-y-4">
        <div className="rounded-lg border border-gray-800 bg-gray-900 p-4">
          <h2 className="mb-1 text-sm font-semibold text-gray-300">Server</h2>
          <p className="text-sm text-gray-500">farouter · port 20180</p>
        </div>
        <div className="rounded-lg border border-gray-800 bg-gray-900 p-4">
          <h2 className="mb-1 text-sm font-semibold text-gray-300">Theme</h2>
          <p className="text-sm text-gray-500">Dark mode only</p>
        </div>
      </div>
    </section>
  );
};

export default Settings;
