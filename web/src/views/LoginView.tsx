import { FormEvent, useState } from "react";
import { useLocation } from "wouter";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
import { api } from "../api";

interface LoginViewProps {
  onLoginSuccess: () => void;
}

export function LoginView({ onLoginSuccess }: LoginViewProps) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [, setLocation] = useLocation();

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      await api.login(password);
      onLoginSuccess();
      setLocation("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4">
      <div className="max-w-md w-full">
        <Card>
          <CardHeader className="text-center space-y-2">
            <CardTitle className="text-3xl">Marina Backup</CardTitle>
            <CardDescription>Please sign in to continue</CardDescription>
          </CardHeader>
          <CardContent>
            {error && (
              <div className="mb-6 p-4 bg-red-50 border border-red-200 rounded-md">
                <p className="text-sm text-red-600">{error}</p>
              </div>
            )}

            <form onSubmit={handleSubmit} className="space-y-6">
              <div className="space-y-2">
                <label
                  htmlFor="password"
                  className="text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70"
                >
                  Password
                </label>
                <Input
                  type="password"
                  id="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Enter password"
                  required
                  autoFocus
                  disabled={loading}
                />
              </div>

              <Button type="submit" disabled={loading} className="w-full" size="lg">
                {loading ? "Signing in..." : "Sign In"}
              </Button>
            </form>
          </CardContent>
        </Card>

        <div className="mt-6 text-center text-sm text-gray-500">
          <p>Marina - Docker Backup Orchestrator</p>
        </div>
      </div>
    </div>
  );
}
