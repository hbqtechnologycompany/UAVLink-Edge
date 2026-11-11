(function () {
    // Nhãn board (CM5 / CM2) cho mọi trang: phần tử .db-board nhận board.label,
    // .db-board-name nhận board.name; script trang dùng window.dbBoardLabel().
    var board = null;

    function applyBoard(next) {
        if (!next || !next.label) return;
        board = next;
        document.documentElement.setAttribute('data-board', board.id);
        document.querySelectorAll('.db-board').forEach(function (el) { el.textContent = board.label; });
        document.querySelectorAll('.db-board-name').forEach(function (el) { el.textContent = board.name; });
        document.dispatchEvent(new CustomEvent('db-board', { detail: board }));
    }

    window.dbBoardLabel = function () { return board ? board.label : 'board'; };
    window.dbBoard = function () { return board; };

    window.addEventListener('message', function (e) {
        if (e.origin === location.origin && e.data && e.data.type === 'db-board') applyBoard(e.data.board);
    });

    function loadBoard() {
        if (board) return;
        fetch('/api/system/board', { cache: 'no-store' })
            .then(function (r) { return r.ok ? r.json() : null; })
            .then(applyBoard)
            .catch(function () { /* giữ nhãn mặc định */ });
    }
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', loadBoard);
    } else {
        loadBoard();
    }

    if (new URLSearchParams(window.location.search).get('embed') !== '1') return;

    document.documentElement.classList.add('db-embed');

    var LEGACY_THEMES = {
        ocean: 'sky', forest: 'sand', purple: 'graphite', carbon: 'midnight', neon: 'graphite', indigo: 'graphite'
    };

    function applyShellTheme(name) {
        if (LEGACY_THEMES[name]) name = LEGACY_THEMES[name];
        var allowed = ['light', 'sky', 'sand', 'midnight', 'graphite'];
        if (allowed.indexOf(name) < 0) name = 'light';
        document.documentElement.setAttribute('data-theme', name);
    }

    var themeParam = new URLSearchParams(window.location.search).get('shellTheme');
    if (themeParam) applyShellTheme(themeParam);

    var scrollPages = ['mavlink.html', 'connect.html', 'mavlink_settings.html', 'settings.html', 'tokens.html', 'preflight.html'];
    var page = window.location.pathname.split('/').pop() || '';
    if (scrollPages.some(function (p) { return page.indexOf(p.replace('.html', '')) >= 0; })) {
        document.documentElement.classList.add('db-scroll');
    }

    window.addEventListener('message', function (e) {
        if (e.data && e.data.type === 'db-theme' && e.data.theme) {
            applyShellTheme(e.data.theme);
        }
    });

    document.addEventListener('DOMContentLoaded', function () {
        document.body.classList.add('db-embed');
    });
})();
