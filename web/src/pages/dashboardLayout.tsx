import { Outlet, Link } from 'react-router';

const navLinks = [
  { label: 'Overview', path: '/dashboard' },
  { label: 'Settings', path: '/dashboard/settings' },
];

const DashboardLayout = () => {
  return (
    <div className="flex h-screen bg-gray-950 text-gray-100">
      <aside className="fixed left-0 top-0 h-screen w-56 border-r border-gray-800 bg-gray-900 p-4">
        <h2 className="mb-6 text-lg font-bold text-cyan-400">farouter</h2>
        <nav className="space-y-1">
          {navLinks.map((l) => (
            <Link
              key={l.path}
              to={l.path}
              className="block rounded px-3 py-2 text-sm text-gray-400 hover:bg-gray-800 hover:text-gray-200"
            >
              {l.label}
            </Link>
          ))}
        </nav>
      </aside>

      <header className="fixed left-56 right-0 top-0 z-10 border-b border-gray-800 bg-gray-900/80 px-6 py-3 backdrop-blur">
        <div className="flex items-center justify-between">
          <span className="text-sm text-gray-400">Dashboard</span>
        </div>
      </header>

      <main className="ml-56 mt-14 flex-1 overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  );
};

export default DashboardLayout;
