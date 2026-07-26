const About = () => {
  return (
    <section>
      <h1 className="mb-4 text-3xl font-bold">About</h1>
      <div className="space-y-4 text-gray-400">
        <p>
          <strong className="text-gray-200">farouter</strong> is a Go port of the Kiro
          executor — an OpenAI-compatible proxy that routes chat completion requests to
          the Amazon Q Developer (Kiro) upstream service.
        </p>
        <div className="rounded-lg border border-gray-800 bg-gray-900 p-4">
          <h2 className="mb-2 text-lg font-semibold text-cyan-400">Features</h2>
          <ul className="list-disc space-y-1 pl-5 text-sm">
            <li>Multi-account rotation with sticky sessions</li>
            <li>Boot-time quota &amp; suspension detection</li>
            <li>Inline &lt;thinking&gt; tag splitting</li>
            <li>Auto-retry on 402/403 responses</li>
            <li>Persistent account state</li>
          </ul>
        </div>
      </div>
    </section>
  );
};

export default About;
