'use strict';

document.addEventListener('DOMContentLoaded', function () {
    var form = document.getElementById('vm-inspection-settings-form');
    if (form) {
        var globalState = document.getElementById('isolation_enabled');
        var rows = Array.prototype.slice.call(document.querySelectorAll('.vm-inspection-node'));
        var count = document.getElementById('selected-node-count');
        var selectAll = document.getElementById('select-visible-nodes');

        function updateRow(row) {
            var state = row.querySelector('.node-state');
            var changed = state.value !== state.getAttribute('data-original');
            if (changed) {
                state.name = 'node_' + state.getAttribute('data-hyper-id');
            } else {
                state.removeAttribute('name');
            }
            row.classList.toggle('warning', changed);
            var effective = state.value === 'default' ? globalState.value : state.value;
            var cell = row.querySelector('.node-effective');
            cell.textContent = cell.getAttribute(effective === 'true' ? 'data-enabled' : 'data-disabled');
        }

        function updateSelection() {
            var selected = rows.filter(function (row) { return row.querySelector('.node-selected').checked; });
            var visible = rows.filter(function (row) { return !row.hidden; });
            var checked = visible.filter(function (row) { return row.querySelector('.node-selected').checked; });
            count.textContent = selected.length;
            selectAll.checked = visible.length > 0 && checked.length === visible.length;
            selectAll.indeterminate = checked.length > 0 && checked.length < visible.length;
        }

        rows.forEach(function (row) {
            updateRow(row);
            row.querySelector('.node-state').addEventListener('change', function () { updateRow(row); });
            row.querySelector('.node-selected').addEventListener('change', updateSelection);
        });
        globalState.addEventListener('change', function () { rows.forEach(updateRow); });
        document.getElementById('node-search').addEventListener('input', function () {
            var query = this.value.trim().toLowerCase();
            rows.forEach(function (row) {
                row.hidden = row.getAttribute('data-search').toLowerCase().indexOf(query) === -1;
            });
            updateSelection();
        });
        selectAll.addEventListener('change', function () {
            var checked = this.checked;
            rows.forEach(function (row) { if (!row.hidden) { row.querySelector('.node-selected').checked = checked; } });
            updateSelection();
        });
        document.getElementById('apply-node-state').addEventListener('click', function () {
            var state = document.getElementById('batch-node-state').value;
            rows.forEach(function (row) {
                if (row.querySelector('.node-selected').checked) {
                    row.querySelector('.node-state').value = state;
                    updateRow(row);
                }
            });
        });
    }

    var syncButtons = Array.prototype.slice.call(document.querySelectorAll('.vm-infection-sync'));
    var syncPage = document.getElementById('vm-infection-sync-page');
    var syncStatus = document.getElementById('vm-infection-sync-status');

    async function syncRecord(button) {
        var reply = await fetch(button.getAttribute('data-url'), {
            method: 'POST', credentials: 'same-origin',
            headers: { 'X-Csrf-Token': button.getAttribute('data-csrf'), 'Accept': 'application/json' }
        });
        if (reply.status !== 202) { throw new Error(syncStatus.getAttribute('data-failed')); }
        var accepted = await reply.json();
        for (var attempt = 0; attempt < 30; attempt++) {
            await new Promise(function (resolve) { setTimeout(resolve, 2000); });
            var current = await fetch(button.getAttribute('data-status-url'), {
                credentials: 'same-origin', cache: 'no-store', headers: { 'Accept': 'application/json' }
            });
            if (!current.ok) { throw new Error(syncStatus.getAttribute('data-failed')); }
            var record = await current.json();
            if (record.updated_at !== accepted.updated_at) { return; }
        }
        throw new Error(syncStatus.getAttribute('data-timeout'));
    }

    async function syncRecords(buttons) {
        syncButtons.forEach(function (button) { button.disabled = true; });
        if (syncPage) { syncPage.disabled = true; }
        syncStatus.hidden = false;
        syncStatus.textContent = syncStatus.getAttribute('data-pending');
        // All requests use the same read-result API as a single-row sync.
        var results = await Promise.all(buttons.map(function (button) {
            return syncRecord(button).then(function () { return null; }, function (error) { return error.message; });
        }));
        var failures = results.filter(function (result) { return result !== null; });
        if (failures.length < results.length) {
            var url = new URL(window.location.href);
            url.searchParams.set('synced', '1');
            if (failures.length) { url.searchParams.set('sync_failed', String(failures.length)); }
            else { url.searchParams.delete('sync_failed'); }
            window.location.assign(url.toString());
            return;
        }
        syncStatus.textContent = failures[0] + ' (' + (results.length - failures.length) + '/' + results.length + ')';
        syncButtons.forEach(function (button) { button.disabled = false; });
        if (syncPage) { syncPage.disabled = false; }
    }
    syncButtons.forEach(function (button) {
        button.addEventListener('click', function () { syncRecords([button]); });
    });
    if (syncPage) { syncPage.addEventListener('click', function () { syncRecords(syncButtons); }); }

    document.querySelectorAll('form[data-confirm]').forEach(function (actionForm) {
        actionForm.addEventListener('submit', function (event) {
            if (!window.confirm(actionForm.getAttribute('data-confirm'))) {
                event.preventDefault();
            }
        });
    });
});
