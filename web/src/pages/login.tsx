import { useState } from 'react';
import { useNavigate, redirect } from 'react-router';

export async function loader() {
  const res = await fetch('/api/verify');
  if (res.ok) return redirect('/dashboard');
  return null;
}

const Login = () => {
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const nav = useNavigate();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    });
    if (!res.ok) {
      setError('Wrong password');
      return;
    }
    nav('/dashboard');
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-950">
      <form onSubmit={submit} className="w-full max-w-sm rounded-lg border border-gray-800 bg-gray-900 p-6">
        <h1 className="mb-6 text-center text-2xl font-bold text-cyan-400">farouter</h1>
        <input
          type="password"
          placeholder="Password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mb-4 w-full rounded border border-gray-700 bg-gray-800 px-3 py-2 text-gray-100 placeholder-gray-500 focus:border-cyan-500 focus:outline-none"
          autoFocus
        />
        {error && <p className="mb-4 text-sm text-red-400">{error}</p>}
        <button
          type="submit"
          className="w-full rounded bg-cyan-600 px-4 py-2 font-medium text-white hover:bg-cyan-500"
        >
          Login
        </button>
      </form>
    </div>
  );
};

export default Login;
