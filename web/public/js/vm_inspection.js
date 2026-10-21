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

    document.querySelectorAll('form[data-confirm]').forEach(function (actionForm) {
        actionForm.addEventListener('submit', function (event) {
            if (!window.confirm(actionForm.getAttribute('data-confirm'))) {
                event.preventDefault();
            }
        });
    });
});
