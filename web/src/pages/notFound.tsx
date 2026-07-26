import { Link } from 'react-router';

const NotFound = () => {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-gray-950 text-gray-100">
      <h1 className="mb-2 text-6xl font-bold text-cyan-400">404</h1>
      <p className="mb-6 text-gray-400">Page not found</p>
      <Link to="/" className="text-sm text-cyan-500 hover:underline">Back to home</Link>
    </div>
  );
};

export default NotFound;
