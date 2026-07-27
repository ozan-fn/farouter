import { Outlet, Link, useLocation } from 'react-router';

const navLinks = [
  { label: 'Overview', path: '/dashboard' },
  { label: 'Quotas', path: '/dashboard/quotas' },
  { label: 'Analytics', path: '/dashboard/analytics' },
  { label: 'Monitoring', path: '/dashboard/monitoring' },
  { label: 'Settings', path: '/dashboard/settings' },
];

const DashboardLayout = () => {
  const location = useLocation();

  return (
    <div className="flex h-screen bg-gray-950 text-gray-100">
      <aside className="fixed left-0 top-0 h-screen w-64 border-r border-gray-800 bg-gray-900/40 p-4">
        <h2 className="mb-6 text-lg font-bold text-cyan-400">farouter</h2>
        <nav className="space-y-1">
          {navLinks.map((l) => {
            const isActive = location.pathname === l.path;
            return (
              <Link
                key={l.path}
                to={l.path}
                className={`block px-3 py-2 text-sm transition ${
                  isActive
                    ? 'bg-cyan-500/20 text-cyan-400 border-l-2 border-cyan-500'
                    : 'text-gray-400 hover:bg-gray-800/60 hover:text-gray-200'
                }`}
              >
                {l.label}
              </Link>
            );
          })}
        </nav>
      </aside>

      <header className="fixed left-64 right-0 top-0 z-10 h-16 border-b border-gray-800 bg-gray-900/60 px-6 backdrop-blur flex items-center">
        <span className="text-sm text-gray-400">Dashboard</span>
      </header>

      <main className="ml-64 mt-16 flex-1 overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  );
};

export default DashboardLayout;
