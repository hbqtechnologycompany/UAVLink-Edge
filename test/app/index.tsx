import React, { useState, useEffect, useMemo } from 'react';
import { createRoot } from 'react-dom/client';
import { 
  Shield, 
  Plus, 
  Clock, 
  History, 
  User, 
  CheckCircle2, 
  Copy, 
  AlertCircle, 
  Key, 
  RefreshCw,
  Calendar,
  LogOut,
  Search,
  Filter,
  Eye,
  EyeOff,
  Settings,
  X,
  Zap
} from 'lucide-react';

// --- Types ---

interface Token {
  id: string;
  tokenKey: string;
  createdAt: string; // ISO Date string
  expiresAt: string; // ISO Date string
  status: 'active' | 'expired' | 'revoked';
  lastState: 'connected' | 'pending';
  connectedUser?: string;
}

type ViewState = 'dashboard' | 'history';

// --- Constants & Helpers ---

const generateTokenString = () => {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  const prefix = 'sk_live_';
  let result = '';
  for (let i = 0; i < 24; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return prefix + result;
};

// --- Mock Data ---

const INITIAL_HISTORY: Token[] = [
  {
    id: '1',
    tokenKey: 'sk_live_OldTokenExpired123',
    createdAt: new Date(Date.now() - 5 * 24 * 60 * 60 * 1000).toISOString(),
    expiresAt: new Date(Date.now() - 4 * 24 * 60 * 60 * 1000).toISOString(),
    status: 'expired',
    lastState: 'connected',
    connectedUser: 'dev_team_alpha'
  },
  {
    id: '2',
    tokenKey: 'sk_live_ActiveTokenExample99',
    createdAt: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    expiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(),
    status: 'active',
    lastState: 'connected',
    connectedUser: 'system_admin'
  },
  {
    id: '3',
    tokenKey: 'sk_live_PendingTokenTest001',
    createdAt: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
    expiresAt: new Date(Date.now() + 2 * 24 * 60 * 60 * 1000).toISOString(),
    status: 'active',
    lastState: 'pending'
  }
];

// --- Components ---

const App: React.FC = () => {
  const [tokens, setTokens] = useState<Token[]>(INITIAL_HISTORY);
  const [currentView, setCurrentView] = useState<ViewState>('dashboard');
  
  // Create Modal State
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);
  const [createDays, setCreateDays] = useState<number>(1);
  const [createHours, setCreateHours] = useState<number>(0);

  // Adjust Modal State
  const [adjustToken, setAdjustToken] = useState<Token | null>(null);
  const [newExpiryDate, setNewExpiryDate] = useState<string>('');

  // Visibility State
  const [visibleTokens, setVisibleTokens] = useState<Set<string>>(new Set());

  // Search
  const [searchQuery, setSearchQuery] = useState('');

  // Auto-expire tokens logic
  useEffect(() => {
    const interval = setInterval(() => {
      setTokens(prevTokens => prevTokens.map(t => {
        if (t.status === 'active' && new Date(t.expiresAt).getTime() < Date.now()) {
          return { ...t, status: 'expired' };
        }
        return t;
      }));
    }, 60000); 
    return () => clearInterval(interval);
  }, []);

  const activeTokens = useMemo(() => {
    return tokens
      .filter(t => t.status === 'active' && new Date(t.expiresAt).getTime() > Date.now())
      .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
  }, [tokens]);

  const historyTokens = useMemo(() => {
    return tokens
      .filter(t => t.status !== 'active' || new Date(t.expiresAt).getTime() <= Date.now())
      .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
  }, [tokens]);

  const filteredHistory = useMemo(() => {
    return historyTokens.filter(t => 
      t.tokenKey.toLowerCase().includes(searchQuery.toLowerCase()) || 
      (t.connectedUser && t.connectedUser.toLowerCase().includes(searchQuery.toLowerCase()))
    );
  }, [historyTokens, searchQuery]);

  const isValidDuration = (d: number, h: number) => {
    const totalMinutes = d * 24 * 60 + h * 60;
    // Min 1 hour (60 mins), Max 30 days
    return totalMinutes >= 60 && d <= 30;
  };

  const getDurationMs = (d: number, h: number) => {
    return (d * 24 * 60 * 60 * 1000) + (h * 60 * 60 * 1000);
  };

  const handleCreateToken = () => {
    if (!isValidDuration(createDays, createHours)) {
      alert("Thời gian không hợp lệ! Tối thiểu 1 giờ, tối đa 30 ngày.");
      return;
    }

    const now = new Date();
    const expiry = new Date(now.getTime() + getDurationMs(createDays, createHours));

    const newToken: Token = {
      id: crypto.randomUUID(),
      tokenKey: generateTokenString(),
      createdAt: now.toISOString(),
      expiresAt: expiry.toISOString(),
      status: 'active',
      lastState: 'pending',
    };

    setTokens(prev => [newToken, ...prev]);
    setIsCreateModalOpen(false);
    // Reset defaults
    setCreateDays(1);
    setCreateHours(0);
  };

  const handleRevoke = (id: string) => {
    if (!confirm('Bạn có chắc chắn muốn hủy token này không? Hành động này không thể hoàn tác.')) return;
    setTokens(prev => prev.map(t => t.id === id ? { ...t, status: 'revoked' } : t));
  };

  const openAdjustModal = (token: Token) => {
    setAdjustToken(token);
    // Convert current expiry to datetime-local string format (YYYY-MM-DDTHH:mm)
    // Note: This needs to handle timezone offset for the input to show correct local time
    const date = new Date(token.expiresAt);
    const offset = date.getTimezoneOffset() * 60000;
    const localISOTime = (new Date(date.getTime() - offset)).toISOString().slice(0, 16);
    setNewExpiryDate(localISOTime);
  };

  const handleSaveAdjust = () => {
    if (!adjustToken || !newExpiryDate) return;
    
    const newDate = new Date(newExpiryDate);
    if (newDate.getTime() <= Date.now()) {
      alert("Thời gian hết hạn phải lớn hơn thời gian hiện tại!");
      return;
    }

    setTokens(prev => prev.map(t => t.id === adjustToken.id ? { ...t, expiresAt: newDate.toISOString() } : t));
    setAdjustToken(null);
  };

  const toggleVisibility = (id: string) => {
    setVisibleTokens(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
  };

  const formatDate = (isoString: string) => {
    return new Date(isoString).toLocaleString('vi-VN', {
      day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit'
    });
  };

  const getTimeRemaining = (expiresAt: string) => {
    const total = Date.parse(expiresAt) - Date.now();
    if (total <= 0) return "Expired";
    
    const days = Math.floor(total / (1000 * 60 * 60 * 24));
    const hours = Math.floor((total / (1000 * 60 * 60)) % 24);
    const minutes = Math.floor((total / 1000 / 60) % 60);

    if (days > 0) return `${days}d ${hours}h`;
    return `${hours}h ${minutes}m`;
  };

  return (
    <div className="min-h-screen bg-slate-950 flex flex-col md:flex-row font-sans text-slate-200">
      
      {/* Sidebar Navigation */}
      <aside className="w-full md:w-64 bg-slate-900 border-r border-slate-800 flex flex-col flex-shrink-0 z-20">
        <div className="p-6 border-b border-slate-800 flex items-center gap-3">
          <div className="w-10 h-10 bg-cyan-500/10 rounded-xl flex items-center justify-center border border-cyan-500/50 shadow-[0_0_15px_rgba(6,182,212,0.2)]">
            <Shield className="text-cyan-400" size={24} />
          </div>
          <div>
            <span className="text-lg font-bold tracking-tight text-white block">SecureToken</span>
            <span className="text-[10px] text-slate-500 uppercase tracking-widest font-semibold">Manager</span>
          </div>
        </div>

        <div className="p-4 space-y-2 flex-1">
          <button 
            onClick={() => setCurrentView('dashboard')}
            className={`w-full flex items-center gap-3 px-4 py-3 rounded-lg transition-all ${currentView === 'dashboard' ? 'bg-slate-800/80 text-cyan-400 border border-slate-700/50 shadow-sm' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/30'}`}
          >
            <Key size={18} />
            <span className="font-medium text-sm">API Keys</span>
          </button>
          <button 
            onClick={() => setCurrentView('history')}
            className={`w-full flex items-center gap-3 px-4 py-3 rounded-lg transition-all ${currentView === 'history' ? 'bg-slate-800/80 text-cyan-400 border border-slate-700/50 shadow-sm' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/30'}`}
          >
            <History size={18} />
            <span className="font-medium text-sm">Lịch sử (Audit Log)</span>
          </button>
        </div>

        <div className="p-4 border-t border-slate-800 bg-slate-900/50">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-full bg-slate-700 flex items-center justify-center border border-slate-600">
              <User size={16} className="text-slate-300" />
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-white truncate">Admin</p>
              <p className="text-xs text-slate-500 truncate">admin@system.local</p>
            </div>
          </div>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex-1 flex flex-col min-w-0 h-screen overflow-hidden bg-slate-950 relative">
        {/* Background Decorative Elements */}
        <div className="absolute top-0 left-0 w-full h-96 bg-gradient-to-b from-cyan-900/10 to-transparent pointer-events-none" />

        {/* Header */}
        <header className="h-20 border-b border-slate-800 bg-slate-950/80 backdrop-blur-md flex items-center justify-between px-8 shrink-0 z-10">
          <h1 className="text-2xl font-bold text-slate-100 flex items-center gap-2">
            {currentView === 'dashboard' ? 'API Keys' : 'Nhật ký hoạt động'}
          </h1>
          {currentView === 'dashboard' && (
            <button 
              onClick={() => setIsCreateModalOpen(true)}
              className="flex items-center gap-2 bg-cyan-600 hover:bg-cyan-500 text-white px-5 py-2.5 rounded-lg text-sm font-medium transition-colors shadow-lg shadow-cyan-900/20 active:scale-95"
            >
              <Plus size={18} />
              <span>Create API Key</span>
            </button>
          )}
        </header>

        <div className="flex-1 overflow-y-auto p-8 scroll-smooth z-0">
          <div className="max-w-6xl mx-auto">
            
            {/* VIEW: DASHBOARD (Active Tokens List) */}
            {currentView === 'dashboard' && (
              <div className="space-y-6">
                <div className="flex justify-between items-end mb-2">
                   <p className="text-slate-400 text-sm">
                     Quản lý các token đang hoạt động. Bạn có thể điều chỉnh thời hạn hoặc hủy quyền truy cập bất cứ lúc nào.
                   </p>
                </div>

                <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow-xl">
                  {activeTokens.length === 0 ? (
                    <div className="flex flex-col items-center justify-center py-20 text-slate-500">
                      <div className="w-16 h-16 bg-slate-800/50 rounded-full flex items-center justify-center mb-4">
                        <Key size={32} className="opacity-50" />
                      </div>
                      <h3 className="text-lg font-medium text-slate-300 mb-1">Chưa có API Key nào</h3>
                      <p className="text-sm max-w-sm text-center mb-6">Tạo key mới để cấp quyền truy cập cho ứng dụng hoặc người dùng.</p>
                      <button 
                        onClick={() => setIsCreateModalOpen(true)}
                        className="text-cyan-400 hover:text-cyan-300 text-sm font-medium"
                      >
                        + Tạo key đầu tiên
                      </button>
                    </div>
                  ) : (
                    <table className="w-full text-left border-collapse">
                      <thead>
                        <tr className="border-b border-slate-800 bg-slate-950/30">
                          <th className="p-4 pl-6 text-xs font-semibold text-slate-500 uppercase tracking-wider w-1/3">Token Key</th>
                          <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Trạng thái kết nối</th>
                          <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Ngày tạo</th>
                          <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Hết hạn</th>
                          <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider text-right pr-6">Tác vụ</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-800/50">
                        {activeTokens.map((token) => (
                          <tr key={token.id} className="group hover:bg-slate-800/20 transition-colors">
                            <td className="p-4 pl-6">
                              <div className="flex items-center gap-3">
                                <div className="p-2 bg-slate-800 rounded-lg">
                                  <Key size={16} className="text-cyan-400" />
                                </div>
                                <div className="flex-1 min-w-0">
                                  <div className="flex items-center gap-2 mb-1">
                                    <span className="font-mono text-sm text-slate-200 font-medium tracking-tight w-[280px] inline-block truncate align-bottom">
                                      {visibleTokens.has(token.id) ? token.tokenKey : 'sk_live_••••••••••••••••••••••••'}
                                    </span>
                                    <button 
                                      onClick={() => toggleVisibility(token.id)}
                                      className="text-slate-600 hover:text-slate-400 transition-colors"
                                      title={visibleTokens.has(token.id) ? "Ẩn" : "Hiện"}
                                    >
                                      {visibleTokens.has(token.id) ? <EyeOff size={14} /> : <Eye size={14} />}
                                    </button>
                                    <button 
                                      onClick={() => copyToClipboard(token.tokenKey)}
                                      className="text-slate-600 hover:text-cyan-400 transition-colors ml-1"
                                      title="Copy"
                                    >
                                      <Copy size={14} />
                                    </button>
                                  </div>
                                  <div className="flex items-center gap-2">
                                     <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                                      Active
                                     </span>
                                  </div>
                                </div>
                              </div>
                            </td>
                            <td className="p-4">
                              {token.lastState === 'connected' ? (
                                <div className="flex flex-col">
                                  <span className="flex items-center gap-1.5 text-sm text-emerald-400 font-medium">
                                    <CheckCircle2 size={14} /> Connected
                                  </span>
                                  {token.connectedUser && (
                                    <span className="text-xs text-slate-500 mt-0.5 flex items-center gap-1">
                                      <User size={10} /> {token.connectedUser}
                                    </span>
                                  )}
                                </div>
                              ) : (
                                <span className="flex items-center gap-1.5 text-sm text-slate-500">
                                  <Clock size={14} /> Pending Connection
                                </span>
                              )}
                            </td>
                            <td className="p-4">
                               <span className="text-sm text-slate-300 font-mono">{new Date(token.createdAt).toLocaleDateString('vi-VN')}</span>
                               <div className="text-xs text-slate-600">{new Date(token.createdAt).toLocaleTimeString('vi-VN', {hour:'2-digit', minute:'2-digit'})}</div>
                            </td>
                            <td className="p-4">
                              <div className="flex flex-col">
                                <span className="text-sm text-slate-300 font-mono">{new Date(token.expiresAt).toLocaleDateString('vi-VN')}</span>
                                <span className="text-xs text-orange-400/80 mt-0.5 flex items-center gap-1">
                                  <Zap size={10} /> {getTimeRemaining(token.expiresAt)} remaining
                                </span>
                              </div>
                            </td>
                            <td className="p-4 pr-6 text-right">
                              <div className="flex items-center justify-end gap-2 opacity-80 group-hover:opacity-100 transition-opacity">
                                <button 
                                  onClick={() => openAdjustModal(token)}
                                  className="p-2 text-slate-400 hover:text-white hover:bg-slate-800 rounded-lg transition-all border border-transparent hover:border-slate-700"
                                  title="Điều chỉnh hạn (Adjust Expiry)"
                                >
                                  <Settings size={16} />
                                </button>
                                <button 
                                  onClick={() => handleRevoke(token.id)}
                                  className="p-2 text-red-500/80 hover:text-red-400 hover:bg-red-950/30 rounded-lg transition-all border border-transparent hover:border-red-900/30"
                                  title="Hủy Token (Revoke)"
                                >
                                  <LogOut size={16} />
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </div>
            )}

            {/* VIEW: HISTORY (Audit Log) */}
            {currentView === 'history' && (
              <div className="space-y-4 animate-in fade-in duration-300">
                <div className="flex items-center justify-between bg-slate-900 p-4 rounded-xl border border-slate-800 shadow-sm">
                  <div className="flex items-center gap-2 text-slate-400">
                     <Filter size={16} />
                     <span className="text-sm font-medium">Lọc lịch sử</span>
                  </div>
                  <div className="relative">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" size={14} />
                    <input 
                      type="text" 
                      placeholder="Tìm theo User hoặc ID..." 
                      value={searchQuery}
                      onChange={(e) => setSearchQuery(e.target.value)}
                      className="bg-slate-950 border border-slate-800 rounded-lg pl-9 pr-3 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-cyan-500/50 w-64 placeholder:text-slate-600"
                    />
                  </div>
                </div>

                <div className="bg-slate-900 border border-slate-800 rounded-2xl overflow-hidden shadow-lg">
                  <table className="w-full text-left border-collapse">
                    <thead>
                      <tr className="border-b border-slate-800 bg-slate-950/50">
                        <th className="p-4 pl-6 text-xs font-semibold text-slate-500 uppercase tracking-wider">Token Key</th>
                        <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Trạng thái</th>
                        <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Thời gian tạo</th>
                        <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider">Thời gian đóng</th>
                        <th className="p-4 text-xs font-semibold text-slate-500 uppercase tracking-wider text-right pr-6">User</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-800">
                      {filteredHistory.length === 0 ? (
                        <tr>
                          <td colSpan={5} className="p-12 text-center text-slate-500">
                            <History size={32} className="mx-auto mb-2 opacity-50" />
                            Không tìm thấy dữ liệu lịch sử.
                          </td>
                        </tr>
                      ) : (
                        filteredHistory.map((token) => (
                          <tr key={token.id} className="hover:bg-slate-800/30 transition-colors">
                            <td className="p-4 pl-6">
                              <span className="font-mono text-xs text-slate-400">
                                {token.tokenKey.substring(0, 24)}...
                              </span>
                            </td>
                            <td className="p-4">
                              {token.status === 'revoked' && (
                                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium bg-red-950/20 text-red-400 border border-red-900/20">Revoked</span>
                              )}
                              {token.status === 'expired' && (
                                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium bg-slate-800 text-slate-500 border border-slate-700">Expired</span>
                              )}
                            </td>
                            <td className="p-4">
                              <span className="text-xs text-slate-400 font-mono">{formatDate(token.createdAt)}</span>
                            </td>
                            <td className="p-4">
                              <span className="text-xs text-slate-400 font-mono">{formatDate(token.expiresAt)}</span>
                            </td>
                            <td className="p-4 pr-6 text-right">
                              {token.connectedUser ? (
                                <span className="text-xs text-slate-300 font-medium">{token.connectedUser}</span>
                              ) : (
                                <span className="text-slate-600 text-xs italic">--</span>
                              )}
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            )}

            {/* --- MODALS --- */}

            {/* Create Token Modal */}
            {isCreateModalOpen && (
              <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4">
                <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-lg shadow-2xl animate-in zoom-in-95 duration-200">
                  <div className="p-6 border-b border-slate-800 flex justify-between items-center bg-slate-950/50 rounded-t-2xl">
                    <h3 className="text-lg font-bold text-white flex items-center gap-2">
                      <div className="p-1.5 bg-cyan-500/10 rounded-lg">
                        <Plus className="text-cyan-400" size={18} /> 
                      </div>
                      Tạo API Key Mới
                    </h3>
                    <button onClick={() => setIsCreateModalOpen(false)} className="text-slate-500 hover:text-white transition-colors">
                      <X size={20} />
                    </button>
                  </div>
                  
                  <div className="p-6">
                    <div className="mb-6">
                      <label className="block text-xs font-medium text-slate-400 uppercase tracking-wider mb-3">
                        Thời hạn hiệu lực (Duration)
                      </label>
                      <div className="flex gap-4">
                        <div className="flex-1">
                          <div className="flex items-center bg-slate-950 border border-slate-700 rounded-lg focus-within:ring-1 focus-within:ring-cyan-500/50 focus-within:border-cyan-500/50 transition-all overflow-hidden">
                            <input 
                              type="number" 
                              min="0"
                              max="30"
                              value={createDays}
                              onChange={(e) => setCreateDays(Number(e.target.value))}
                              className="w-full bg-transparent text-slate-200 text-sm pl-4 py-3 focus:outline-none font-mono placeholder:text-slate-600"
                            />
                            <span className="text-slate-500 text-xs font-bold px-4 border-l border-slate-800 bg-slate-900/50 py-3 select-none">NGÀY</span>
                          </div>
                        </div>
                        <div className="flex-1">
                          <div className="flex items-center bg-slate-950 border border-slate-700 rounded-lg focus-within:ring-1 focus-within:ring-cyan-500/50 focus-within:border-cyan-500/50 transition-all overflow-hidden">
                            <input 
                              type="number" 
                              min="0"
                              max="23"
                              value={createHours}
                              onChange={(e) => setCreateHours(Number(e.target.value))}
                              className="w-full bg-transparent text-slate-200 text-sm pl-4 py-3 focus:outline-none font-mono placeholder:text-slate-600"
                            />
                            <span className="text-slate-500 text-xs font-bold px-4 border-l border-slate-800 bg-slate-900/50 py-3 select-none">GIỜ</span>
                          </div>
                        </div>
                      </div>
                      <p className="mt-3 text-[11px] text-slate-500 flex items-center gap-1.5">
                        <AlertCircle size={12} />
                        Tối thiểu: 1 giờ • Tối đa: 30 ngày
                      </p>
                    </div>

                    <div className="flex justify-end gap-3 pt-2">
                       <button 
                        onClick={() => setIsCreateModalOpen(false)}
                        className="px-4 py-2.5 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800 transition-colors text-sm font-medium"
                      >
                        Hủy bỏ
                      </button>
                      <button 
                        onClick={handleCreateToken}
                        className="px-6 py-2.5 rounded-lg bg-cyan-600 text-white hover:bg-cyan-500 transition-colors font-medium text-sm shadow-lg shadow-cyan-900/20"
                      >
                        Tạo Token
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* Adjust Expiry Modal */}
            {adjustToken && (
               <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4">
                <div className="bg-slate-900 border border-slate-800 rounded-2xl w-full max-w-md shadow-2xl animate-in zoom-in-95 duration-200">
                  <div className="p-5 border-b border-slate-800 flex justify-between items-center bg-slate-950/50 rounded-t-2xl">
                    <h3 className="text-lg font-bold text-white flex items-center gap-2">
                      <Settings className="text-cyan-400" size={18} /> Điều chỉnh hạn
                    </h3>
                    <button onClick={() => setAdjustToken(null)} className="text-slate-500 hover:text-white transition-colors">
                      <X size={20} />
                    </button>
                  </div>
                  
                  <div className="p-6">
                    <p className="text-slate-400 text-sm mb-5 leading-relaxed">
                      Thiết lập lại thời gian hết hạn chính xác cho token <span className="font-mono text-cyan-400 bg-cyan-950/30 px-1 rounded">{adjustToken.tokenKey.substring(0, 12)}...</span>. Bạn có thể tăng hoặc giảm thời gian.
                    </p>

                    <div className="mb-6">
                      <label className="block text-xs font-medium text-slate-500 uppercase tracking-wider mb-2">
                        Thời điểm hết hạn mới
                      </label>
                      <input 
                        type="datetime-local" 
                        value={newExpiryDate}
                        onChange={(e) => setNewExpiryDate(e.target.value)}
                        className="w-full bg-slate-950 border border-slate-700 text-slate-200 text-sm rounded-lg px-4 py-3 focus:outline-none focus:border-cyan-500/50 focus:ring-1 focus:ring-cyan-500/50 transition-all font-mono [color-scheme:dark]"
                      />
                      <p className="mt-2 text-xs text-slate-500">
                        Hiện tại: {new Date(adjustToken.expiresAt).toLocaleString('vi-VN')}
                      </p>
                    </div>

                    <div className="flex gap-3">
                      <button 
                        onClick={() => setAdjustToken(null)}
                        className="flex-1 py-2.5 rounded-lg bg-slate-800 text-slate-300 hover:bg-slate-700 transition-colors font-medium text-sm"
                      >
                        Hủy
                      </button>
                      <button 
                        onClick={handleSaveAdjust}
                        className="flex-1 py-2.5 rounded-lg bg-cyan-600 text-white hover:bg-cyan-500 transition-colors font-medium text-sm shadow-lg shadow-cyan-900/20"
                      >
                        Cập nhật
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

          </div>
        </div>
      </main>
    </div>
  );
};

// Mount the app
const rootElement = document.getElementById('root');
if (rootElement) {
  const root = createRoot(rootElement);
  root.render(<App />);
}