import { useEffect, useState } from "react";
import { Route, Switch, Link, Redirect } from "wouter";
import { api } from "./api";
import { SchedulesView } from "./components/SchedulesView";
import { JobStatusesView } from "./components/JobStatusesView";
import { JobDetailsView } from "./components/JobDetailsView";
import { SystemLogsView } from "./components/SystemLogsView";
import { LoginView } from "./components/LoginView";

function App() {
  const [authRequired, setAuthRequired] = useState<boolean | null>(null);
  const [authenticated, setAuthenticated] = useState(false);
  const [loading, setLoading] = useState(true);

  async function checkAuth() {
    try {
      const token = localStorage.getItem("marina_auth_token");
      const headers: HeadersInit = {};
      if (token) {
        headers["Authorization"] = `Bearer ${token}`;
      }
      const response = await fetch("/api/auth/check", { headers });
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
      <Switch>
        <Route path="/login">
          <LoginView onLoginSuccess={handleLoginSuccess} />
        </Route>
        <Route>
          <Redirect to="/login" />
        </Route>
      </Switch>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white shadow-sm">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-8">
              <h1 className="text-2xl font-bold text-gray-900">
                Marina Backup Status
              </h1>
              <nav className="flex space-x-4">
                <Link
                  to="/"
                  className="text-sm text-gray-600 hover:text-gray-900 font-medium"
                >
                  Schedules
                </Link>
                <Link
                  to="/logs"
                  className="text-sm text-gray-600 hover:text-gray-900 font-medium"
                >
                  System Logs
                </Link>
              </nav>
            </div>
            {authRequired && authenticated && (
              <button
                onClick={handleLogout}
                className="text-sm text-gray-600 hover:text-gray-900 font-medium"
              >
                Logout
              </button>
            )}
          </div>
        </div>
      </header>

      <main>
        <Switch>
          <Route path="/">
            <SchedulesView />
          </Route>
          <Route path="/logs">
            <SystemLogsView />
          </Route>
          <Route path="/instance/:instanceId/job/:jobId">
            {(params) => (
              <JobDetailsView
                instanceId={params.instanceId}
                jobId={params.jobId}
              />
            )}
          </Route>
          <Route path="/instance/:instanceId">
            {(params) => <JobStatusesView instanceId={params.instanceId} />}
          </Route>
        </Switch>
      </main>
    </div>
  );
}

export default App;
