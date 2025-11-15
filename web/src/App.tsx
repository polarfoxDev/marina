import { useEffect, useState } from "react";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { SchedulesView } from "./components/SchedulesView";
import { JobStatusesView } from "./components/JobStatusesView";
import { JobDetailsView } from "./components/JobDetailsView";
import { LoginView } from "./components/LoginView";

function App() {
  const [authRequired, setAuthRequired] = useState<boolean | null>(null);
  const [authenticated, setAuthenticated] = useState(false);
  const [loading, setLoading] = useState(true);

  async function checkAuth() {
    try {
      const response = await fetch("/api/auth/check");
      if (response.ok) {
        const data = await response.json();
        setAuthRequired(data.authRequired);
        setAuthenticated(data.authenticated);
      }
    } catch (err) {
      console.error("Failed to check auth:", err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    checkAuth();
  }, []);

  async function handleLogout() {
    try {
      const { api } = await import("./api");
      await api.logout();
      setAuthenticated(false);
    } catch (err) {
      console.error("Logout failed:", err);
    }
  }

  function handleLoginSuccess() {
    setAuthenticated(true);
  }

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50">
        <div className="text-gray-500">Loading...</div>
      </div>
    );
  }

  // If auth is required but user is not authenticated, show login
  if (authRequired && !authenticated) {
    return (
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginView onLoginSuccess={handleLoginSuccess} />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      </BrowserRouter>
    );
  }

  return (
    <BrowserRouter>
      <div className="min-h-screen bg-gray-50">
        <header className="bg-white shadow-sm">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4 flex items-center justify-between">
            <h1 className="text-2xl font-bold text-gray-900">
              Marina Backup Status
            </h1>
            {authRequired && authenticated && (
              <button
                onClick={handleLogout}
                className="text-sm text-gray-600 hover:text-gray-900 font-medium"
              >
                Logout
              </button>
            )}
          </div>
        </header>

        <main>
          <Routes>
            <Route path="/" element={<SchedulesView />} />
            <Route path="/instance/:instanceId" element={<JobStatusesView />} />
            <Route
              path="/instance/:instanceId/job/:jobId"
              element={<JobDetailsView />}
            />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}

export default App;
