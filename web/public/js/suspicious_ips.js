$(function () {
    $('.suspicious-ip-actions').dropdown({action: 'hide'});

    $('#suspicious-ips').on('click', '[data-action]', function (event) {
        event.preventDefault();
        var menu = $(this).closest('.suspicious-ip-actions');
        var baseURL = menu.attr('data-base-url');
        var ip = menu.attr('data-ip');
        var action = $(this).attr('data-action');
        menu.dropdown('hide');

        if (action === 'block') {
            var blockForm = document.getElementById('suspicious-block-form');
            blockForm.action = baseURL + '/block';
            blockForm.reset();
            $('button[form="suspicious-block-form"]').prop('disabled', false);
            var blockModal = $('#suspicious-block-modal');
            blockModal.find('.suspicious-selected-ip').text(ip);
            blockModal.modal({onVisible: function () {
                document.getElementById('suspicious-block-timeout').focus();
            }}).modal('show');
        } else if (action === 'delete') {
            document.getElementById('suspicious-delete-form').action = baseURL + '/delete';
            $('button[form="suspicious-delete-form"]').prop('disabled', false);
            var deleteModal = $('#suspicious-delete-modal');
            deleteModal.find('.suspicious-selected-ip').text(ip);
            deleteModal.modal('show');
        } else if (action === 'unblock') {
            var unblockForm = document.getElementById('suspicious-unblock-form');
            unblockForm.action = baseURL + '/unblock';
            menu.addClass('disabled');
            unblockForm.submit();
        }
    });

    $('#suspicious-block-form, #suspicious-delete-form').on('submit', function () {
        $('button[form="' + this.id + '"]').prop('disabled', true);
    });
});
