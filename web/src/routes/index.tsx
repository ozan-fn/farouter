import { createBrowserRouter, redirect } from 'react-router';

import Login, { loader as loginLoader } from '../pages/login';
import DashboardLayout from '../pages/dashboardLayout';
import Dashboard from '../pages/dashboard';
import Settings from '../pages/settings';
import NotFound from '../pages/notFound';

export async function authLoader() {
  const res = await fetch('/api/verify');
  if (!res.ok) return redirect('/');
  return null;
}

export const router = createBrowserRouter([
  { path: '/', Component: Login, loader: loginLoader },
  {
    path: '/dashboard',
    Component: DashboardLayout,
    loader: authLoader,
    children: [
      { index: true, Component: Dashboard },
      { path: 'settings', Component: Settings },
    ],
  },
  { path: '*', Component: NotFound },
]);
