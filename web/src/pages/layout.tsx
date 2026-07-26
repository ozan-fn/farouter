import { NavLink, Outlet } from 'react-router';

const links = [
  { to: '/', label: 'Home' },
  { to: '/about', label: 'About' },
];

const Layout = () => {
  return (
    <div className="min-h-screen bg-gray-950 text-gray-100">
      <nav className="border-b border-gray-800">
        <div className="mx-auto flex max-w-4xl items-center gap-6 px-4 py-3">
          <span className="text-lg font-bold text-cyan-400">farouter</span>
          <div className="flex gap-4">
            {links.map((link) => (
              <NavLink
                key={link.to}
                to={link.to}
                end={link.to === '/'}
                className={({ isActive }) =>
                  isActive ? 'text-cyan-300 font-medium' : 'text-gray-400 hover:text-gray-200'
                }
              >
                {link.label}
              </NavLink>
            ))}
          </div>
        </div>
      </nav>
      <main className="mx-auto max-w-4xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
};

export default Layout;
